package ui

// Dead-UI checks: styles.css class selectors nothing can match, and .dopeui
// node ids no script touches, both usually leftovers from a redesign.
//
// The .dopeui side is walked as the engine's typed tree (engine.Parse), not
// regexed. The JS and CSS sides are plain text — no parser to lean on — so a
// class/id counts as used if its full name appears as a token, or if a string
// literal ending at one of its '-'/camel boundaries composes it dynamically
// (`"kcard-" + kind`, `"cardTab" + view`). The composition rule can hide a
// genuinely dead name sharing a composed prefix — acceptable: these tests catch
// obvious leftovers, they don't prove minimality.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"

	engine "pecheny.me/dopeuikit/ui"
)

// The vocab doesn't type props as id-minting or id-referencing, so the
// knowledge lives here: idMintProps put an id on an element for JS to grab
// (doneid/titleid/badgeid are expander-minted ids), idRefProps point at another
// node's id from within the page.
var idMintProps = map[string]bool{"id": true, "doneid": true, "titleid": true, "badgeid": true}
var idRefProps = map[string]bool{"for": true, "aria-labelledby": true, "aria-controls": true, "aria-describedby": true, "list": true}

func TestNoUnusedCSSClasses(t *testing.T) {
	css, err := os.ReadFile("../../web/assets/static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	// Everything that can put a class on an element: the compiled pages, the
	// shipped JS, and the Go sources that expand primitives / build dynamic pages.
	var corpus strings.Builder
	for _, doc := range parsePages(t) {
		html, err := Compile(doc.name, doc.src)
		if err != nil {
			t.Fatalf("compile %s: %v", doc.name, err)
		}
		corpus.Write(html)
	}
	corpus.WriteString(jsCorpus(t))
	for _, dir := range []string{".", "../server"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			src, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			corpus.Write(src)
		}
	}

	text := corpus.String()
	var unused []string
	for _, class := range cssClassSelectors(string(css)) {
		if strings.Contains(text, class) || composedDynamically(text, class) {
			continue
		}
		unused = append(unused, "."+class)
	}
	if len(unused) > 0 {
		t.Errorf("styles.css has %d class selector(s) nothing references:\n  %s",
			len(unused), strings.Join(unused, "\n  "))
	}
}

func TestNoUnusedPageIDs(t *testing.T) {
	defined := map[string][]string{} // id → pages defining it
	used := map[string]bool{}        // in-page references (label for=, aria idrefs)
	for _, page := range parsePages(t) {
		doc, err := engine.Parse(page.name, page.src)
		if err != nil {
			t.Fatalf("parse %s: %v", page.name, err)
		}
		walkElements(doc.Nodes, func(el *engine.Element) {
			for _, a := range el.Attrs {
				if idMintProps[a.Name] {
					defined[a.Value] = append(defined[a.Value], page.name)
				}
				if idRefProps[a.Name] {
					for _, ref := range strings.Fields(a.Value) {
						used[ref] = true
					}
				}
			}
		})
	}

	js := jsCorpus(t)
	tokens := map[string]bool{}
	for _, tok := range regexp.MustCompile(`[A-Za-z0-9_$-]+`).FindAllString(js, -1) {
		tokens[tok] = true
	}

	var unused []string
	for id, where := range defined {
		if !used[id] && !tokens[id] && !composedDynamically(js, id) {
			unused = append(unused, "#"+id+" ("+strings.Join(where, ", ")+")")
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Errorf("pages define %d id(s) no script references:\n  %s",
			len(unused), strings.Join(unused, "\n  "))
	}
}

type pageSrc struct {
	name string
	src  []byte
}

func parsePages(t *testing.T) []pageSrc {
	paths, err := filepath.Glob("../../web/assets/ui/*.dopeui")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no .dopeui pages found: %v", err)
	}
	pages := make([]pageSrc, 0, len(paths))
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, pageSrc{filepath.Base(p), src})
	}
	return pages
}

func walkElements(nodes []engine.Node, f func(*engine.Element)) {
	for _, n := range nodes {
		walkItem(n, f)
	}
}

func walkItem(it engine.Item, f func(*engine.Element)) {
	switch v := it.(type) {
	case *engine.Element:
		f(v)
		walkElements(v.Block, f)
		for _, inl := range v.Inline {
			walkItem(inl, f)
		}
	case *engine.RunNode:
		for _, inl := range v.Items {
			walkItem(inl, f)
		}
	}
}

// jsCorpus concatenates every script the app ships: xy's static JS plus the
// kit's assets served under /static (login.js, menu.js). Vendored crypto is
// skipped — it touches no DOM.
func jsCorpus(t *testing.T) string {
	var b strings.Builder
	err := filepath.WalkDir("../../web/assets/static", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".js") {
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			b.Write(src)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	kitJS, err := filepath.Glob("../../../dopeuikit/assets/dist/*.js")
	if err != nil || len(kitJS) == 0 {
		t.Fatalf("no kit JS assets found: %v", err)
	}
	for _, p := range kitJS {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(src)
	}
	return b.String()
}

// composedDynamically reports whether the corpus builds the name from a string
// literal ending at one of its boundaries: `"kcard-" + kind` for
// .kcard-question, `"cardTab" + view` for #cardTabText.
func composedDynamically(corpus, name string) bool {
	for i, r := range name {
		if i == 0 || (r != '-' && !unicode.IsUpper(r)) {
			continue
		}
		prefix := name[:i]
		if r == '-' {
			prefix = name[:i+1]
		}
		if strings.Contains(corpus, prefix+`"`) ||
			strings.Contains(corpus, prefix+"'") ||
			strings.Contains(corpus, prefix+"`") ||
			strings.Contains(corpus, prefix+"${") {
			return true
		}
	}
	return false
}

var classSelectorRe = regexp.MustCompile(`\.([A-Za-z_][A-Za-z0-9_-]*)`)

// cssClassSelectors extracts the class names styles.css selects on, ignoring
// dots inside comments, quoted strings and url() (file extensions, decimals).
func cssClassSelectors(css string) []string {
	css = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	css = regexp.MustCompile(`url\([^)]*\)`).ReplaceAllString(css, "url()")
	css = regexp.MustCompile(`"[^"]*"|'[^']*'`).ReplaceAllString(css, `""`)
	seen := map[string]bool{}
	for _, m := range classSelectorRe.FindAllStringSubmatch(css, -1) {
		seen[m[1]] = true
	}
	classes := make([]string, 0, len(seen))
	for c := range seen {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	return classes
}

// classesUsedByJSRe pulls the literal class strings the frontend puts on
// elements: el(tag, {class: "a b"}) and node.className = "a b". Anything
// composed at runtime ("kcard-" + kind) is caught by the prefix rule below
// rather than parsed.
var classesUsedByJSRe = regexp.MustCompile(`\bclass(?:Name)?\s*[:=]\s*"([^"]+)"`)

// dynamicClassPrefixes are class names the JS builds by concatenation or that
// come from the kit's own stylesheet rather than xy's layer, so a missing rule
// in styles.css says nothing.
var dynamicClassPrefixes = []string{"u-", "btn", "input", "menu-", "modal", "appearance-", "card-desc", "hint", "seg", "fld"}

// knownUnstyled are class names that carry no style ON PURPOSE: a JS selector
// hook (querySelector finds the node by it) or a marker left for a future rule.
// Everything else reaching this list is a typo or an invented name, which is
// what the test is for — keep this short and justified.
var knownUnstyled = map[string]bool{
	"kcard-unread":     true, // hook: board.ts finds the dot to remove it
	"tl-edit":          true, // hook: the inline comment editor's textarea
	"lm-grouphead":     true, // marker on a group's header row
	"lm-move-btn":      true,
	"notif-panel-body": true,
	"pv-block":         true,
}

// TestNoUndefinedCSSClasses is TestNoUnusedCSSClasses in the other direction: a
// class the JS puts on an element but NO stylesheet defines. That one renders
// unstyled and silently — which is how a hand-built «лента» ended up wearing
// .tl-item/.tl-body, names nothing had ever styled, while the real card comment
// wears .tl-event/.tl-comment. Dead CSS was already caught; dead class NAMES
// were not.
func TestNoUndefinedCSSClasses(t *testing.T) {
	defined := map[string]bool{}
	for _, path := range []string{"../../web/assets/static/styles.css", "../../../dopeuikit/assets/core.css"} {
		css, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, c := range cssClassSelectors(string(css)) {
			defined[c] = true
		}
	}

	var missing []string
	seen := map[string]bool{}
	for _, m := range classesUsedByJSRe.FindAllStringSubmatch(jsCorpus(t), -1) {
		for _, class := range strings.Fields(m[1]) {
			// A trailing '-' means the literal is the head of a runtime
			// concatenation ("kcard-" + kind), not a class in its own right.
			if defined[class] || seen[class] || knownUnstyled[class] || strings.HasSuffix(class, "-") {
				continue
			}
			skip := false
			for _, p := range dynamicClassPrefixes {
				if strings.HasPrefix(class, p) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			seen[class] = true
			missing = append(missing, "."+class)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("the frontend sets %d class(es) no stylesheet defines:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// ---- proximity: a heading belongs to what is UNDER it ----

var (
	spaceVarRe    = regexp.MustCompile(`--(space-[0-9-]+):\s*(-?\d+)px`)
	ruleRe        = regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)
	marginTopRe   = regexp.MustCompile(`margin-top:\s*([^;]+)`)
	marginBotRe   = regexp.MustCompile(`margin-bottom:\s*([^;]+)`)
	spaceRefRe    = regexp.MustCompile(`var\(--(space-[0-9-]+)\)`)
	negatedCalcRe = regexp.MustCompile(`calc\(\s*([^)]*)\*\s*-1\s*\)`)
	headingNameRe = regexp.MustCompile(`\.[a-z0-9-]*(title|head)[a-z0-9-]*\b|\.section-label\b`)
)

// resolveSpace turns a margin value into px. Understands the --space-N scale and
// calc(x * -1), which is how the kit tucks a heading into its own content.
// Returns ok=false for anything else, which the caller skips rather than guesses.
func resolveSpace(v string, scale map[string]int) (int, bool) {
	v = strings.TrimSpace(v)
	sign := 1
	if m := negatedCalcRe.FindStringSubmatch(v); m != nil {
		sign, v = -1, strings.TrimSpace(m[1])
	}
	if m := spaceRefRe.FindStringSubmatch(v); m != nil {
		px, ok := scale[m[1]]
		return sign * px, ok
	}
	if strings.HasSuffix(v, "px") {
		var px int
		if _, err := fmt.Sscanf(strings.TrimSuffix(v, "px"), "%d", &px); err == nil {
			return sign * px, true
		}
	}
	if v == "0" {
		return 0, true
	}
	return 0, false
}

// TestHeadingsSitWithTheirContent encodes the proximity rule the design system
// already follows and hand-built markup keeps breaking: a section heading must
// have MORE space above it than below, so it reads as belonging to what follows
// rather than to what precedes. The kit's .section-label does this with
// margin-top: --space-5 and a negative margin-bottom; a heading that spaces
// itself evenly (or, worse, only downward) is the bug reported three times as
// «X is closer to the section above than to its own content».
//
// Heuristic on two axes, deliberately: which selectors are headings (name
// contains label/title/head) and which margin values are resolvable. It cannot
// see flex `gap`, so a heading inside a uniform-gap column needs an explicit
// margin-top to pass — which is exactly the fix such a heading needs anyway.
func TestHeadingsSitWithTheirContent(t *testing.T) {
	scale := map[string]int{}
	var css strings.Builder
	for _, path := range []string{"../../../dopeuikit/assets/core.css", "../../web/assets/static/styles.css"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, m := range spaceVarRe.FindAllStringSubmatch(string(b), -1) {
			var px int
			if _, err := fmt.Sscanf(m[2], "%d", &px); err == nil {
				scale[m[1]] = px
			}
		}
		css.Write(b)
	}

	stripped := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css.String(), "")
	var bad []string
	for _, rule := range ruleRe.FindAllStringSubmatch(stripped, -1) {
		sel, body := strings.TrimSpace(rule[1]), rule[2]
		if !headingNameRe.MatchString(sel) || strings.Contains(sel, "@") {
			continue
		}
		mb := marginBotRe.FindStringSubmatch(body)
		if mb == nil {
			continue // says nothing about its own spacing
		}
		bottom, ok := resolveSpace(mb[1], scale)
		if !ok {
			continue
		}
		top := 0
		if mt := marginTopRe.FindStringSubmatch(body); mt != nil {
			if v, ok := resolveSpace(mt[1], scale); ok {
				top = v
			} else {
				continue
			}
		}
		if top <= bottom {
			bad = append(bad, fmt.Sprintf("%s: margin-top %dpx <= margin-bottom %dpx", sel, top, bottom))
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Errorf("%d heading(s) sit no closer to their own content than to the section above:\n  %s",
			len(bad), strings.Join(bad, "\n  "))
	}
}

// A stray control byte in a source file is invisible in review and makes git
// treat the file as BINARY — no diff, no blame. One reached this repo as a
// literal NUL, from a scripted edit where "\x00" in the replacement text was
// interpreted before it was written. Tab, newline and carriage return are the
// only control characters a source file has any business holding.
func TestNoStrayControlBytes(t *testing.T) {
	roots := []string{"../../web/ts", "../../web/assets/ui", "../../web/assets/static/styles.css",
		"../../internal", "../../jstest", "../../scripts", "../../docs"}
	var bad []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "dist" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			switch filepath.Ext(path) {
			case ".ts", ".js", ".go", ".css", ".md", ".dopeui", ".json", ".py":
			default:
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, c := range b {
				if c < 32 && c != '\t' && c != '\n' && c != '\r' {
					bad = append(bad, fmt.Sprintf("%s: byte %d at offset %d", path, c, i))
					return nil // one report per file is enough
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Errorf("%d source file(s) hold a stray control byte:\n  %s", len(bad), strings.Join(bad, "\n  "))
	}
}
