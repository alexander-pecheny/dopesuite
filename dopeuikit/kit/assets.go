package kit

import (
	"fmt"
	"io/fs"
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
	// Fonts is the font directory (the variable noto-sans-*/jetbrains-mono-* woff2),
	// served at /static/fonts/.
	Fonts = assets.Fonts
)

// kitDisk is where an app's dev server finds the kit's sources for hot reload:
// both apps run from their own module directory, a sibling of dopeuikit/.
const kitDisk = "../dopeuikit/assets"

// Assets resolves an app's asset source with the kit's files wired in — core.css
// ahead of the app layer, the fonts, login.js and menu.js — in both embed and
// disk modes. The app supplies its embedded FS and the disk roots that switch
// on dev mode; the kit knows where the kit's files are.
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

// PageSet serves an app's .dopeui pages with one cache policy: in embed mode a
// page compiles once (Warm does them all at startup so a broken page fails
// there, not on a request); in disk mode every call recompiles from disk for
// hot reload.
type PageSet struct {
	source  fs.FS
	noCache bool
	compile func(name string, src []byte) ([]byte, error)
	mu      sync.Mutex
	cache   map[string][]byte
}

// NewPageSet compiles pages read from source with the app's compiler (its
// ui.Compile, which carries the app's vocabulary overlay); noCache is the asset
// layer's disk mode.
func NewPageSet(source fs.FS, noCache bool, compile func(name string, src []byte) ([]byte, error)) *PageSet {
	return &PageSet{source: source, noCache: noCache, compile: compile, cache: map[string][]byte{}}
}

// Bytes returns the compiled HTML of the page at name ("ui/login.dopeui").
func (p *PageSet) Bytes(name string) ([]byte, error) {
	if !p.noCache {
		p.mu.Lock()
		body, ok := p.cache[name]
		p.mu.Unlock()
		if ok {
			return body, nil
		}
	}
	src, err := fs.ReadFile(p.source, name)
	if err != nil {
		return nil, err
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

// Warm compiles every named page up front in embed mode; a no-op in disk mode.
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
