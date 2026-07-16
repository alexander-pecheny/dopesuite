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
	kitJS, err := filepath.Glob("../../../dopeuikit/assets/*.js")
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
