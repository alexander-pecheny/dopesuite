package kit

import (
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"pecheny.me/dopecore/webassets"
	"pecheny.me/dopeuikit/assets"
)

// The kit is the API for the shared static resources; the raw embed lives in the
// assets package. Apps concatenate CoreCSS ahead of their own layer and overlay
// the fonts at /static/fonts/.
var (
	// CoreCSS is the shared design-system stylesheet (core.css).
	CoreCSS = assets.CoreCSS
	// LoginJS is the shared login page script, served at /static/login.js.
	LoginJS = assets.LoginJS
	// MenuJS is the site-wide chrome script (theme boot + ☰ menu), /static/menu.js.
	MenuJS = assets.MenuJS
	// Fonts is the font directory (the variable noto-sans-*/jetbrains-mono-*/
	// inter-fix-ra-* woff2), served at /static/fonts/.
	Fonts = assets.Fonts
)

// kitDisk is where a dev server finds the kit's sources for hot reload: both
// apps run from their module directory, a sibling of dopeuikit/.
const kitDisk = "../dopeuikit/assets"

// Assets resolves an app's asset source with the kit's files — core.css ahead
// of the app layer, the fonts, login.js, menu.js — wired in for both modes;
// the app names its embedded FS and the disk roots that switch on dev mode.
func Assets(embedded fs.FS, diskRoots ...string) *webassets.Assets {
	js := func(name string) webassets.SharedFile {
		return webassets.SharedFile{Path: "/static/" + name, DiskPath: kitDisk + "/dist/" + name, ContentType: "text/javascript; charset=utf-8"}
	}
	login, menu := js("login.js"), js("menu.js")
	login.Bytes, menu.Bytes = LoginJS, MenuJS
	return webassets.New(webassets.Config{
		Embedded:        embedded,
		DiskRoots:       diskRoots,
		CoreCSS:         CoreCSS,
		CoreCSSDiskPath: kitDisk + "/core.css",
		Fonts:           Fonts,
		FontsDiskRoot:   kitDisk,
		Shared:          []webassets.SharedFile{login, menu},
	})
}

// PageSet compiles an app's .dopeui pages: once in embed mode (Warm does them
// all at startup so a broken page fails there), per call in disk mode.
type PageSet struct {
	source   fs.FS
	noCache  bool
	compile  func(name string, src []byte) ([]byte, error)
	mu       sync.Mutex
	cache    map[string][]byte
	provided map[string][]byte
}

// Provide registers a page source that does not come from the app's asset FS
// — the kit's own LoginPage. It wins over a file of the same name.
func (p *PageSet) Provide(name string, src []byte) *PageSet {
	p.mu.Lock()
	p.provided[name] = src
	delete(p.cache, name)
	p.mu.Unlock()
	return p
}

// LoginPage is the shared login page for an app: its tab title and where a
// fresh login lands. Every app serves it at /login through its own compiler.
func LoginPage(title, redirect string) []byte {
	return []byte(strings.NewReplacer("{{title}}", title, "{{redirect}}", redirect).Replace(string(assets.LoginPage)))
}

// loginRedirectChars is every character a destination may be written with: the
// unreserved set, the sub-delimiters, and the few separators a path and a query
// need. Notably absent are `"` and `\`, and that is not URL business but HTML —
// the destination is substituted into the page SOURCE above, before it is
// parsed, so a quote in it would let its holder close the attribute and write
// attributes of their own.
const loginRedirectChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789" +
	"-._~" + "!$&'()*+,;=" + "/?:@%#"

// maxLoginRedirect is longer than any destination an app has and short enough
// that nobody stuffs a page through it.
const maxLoginRedirect = 512

// SafeLoginRedirect is the rule for a post-login destination an app was handed
// rather than chose — the `next` query parameter on /login, which an invitee
// arrives with and an attacker can write. It is accepted only when it is a path
// on this very site: it begins with a single "/", so it carries no scheme and no
// "//host" authority either, and is written in loginRedirectChars alone.
// Anything else answers "", and the app keeps its own default destination.
//
// The rule lives here because the attribute does: both apps stamp the answer
// into LoginPage, and one place to be wrong is better than two.
func SafeLoginRedirect(next string) string {
	if len(next) == 0 || len(next) > maxLoginRedirect || next[0] != '/' || strings.HasPrefix(next, "//") {
		return ""
	}
	for _, r := range next {
		if !strings.ContainsRune(loginRedirectChars, r) {
			return ""
		}
	}
	return next
}

// NewPageSet takes the app's ui.Compile, which carries its vocabulary overlay.
func NewPageSet(source fs.FS, noCache bool, compile func(name string, src []byte) ([]byte, error)) *PageSet {
	return &PageSet{source: source, noCache: noCache, compile: compile, cache: map[string][]byte{}, provided: map[string][]byte{}}
}

// Bytes is the compiled HTML of the page at name ("ui/login.dopeui").
func (p *PageSet) Bytes(name string) ([]byte, error) {
	if !p.noCache {
		p.mu.Lock()
		body, ok := p.cache[name]
		p.mu.Unlock()
		if ok {
			return body, nil
		}
	}
	p.mu.Lock()
	src, ok := p.provided[name]
	p.mu.Unlock()
	if !ok {
		var err error
		if src, err = fs.ReadFile(p.source, name); err != nil {
			return nil, err
		}
	}
	body, err := p.compile(name, src)
	if err != nil {
		return nil, err
	}
	if !p.noCache {
		p.mu.Lock()
		p.cache[name] = body
		p.mu.Unlock()
	}
	return body, nil
}

func (p *PageSet) Warm(names ...string) error {
	if p.noCache {
		return nil
	}
	for _, name := range names {
		if _, err := p.Bytes(name); err != nil {
			return fmt.Errorf("compile %s: %w", name, err)
		}
	}
	return nil
}
