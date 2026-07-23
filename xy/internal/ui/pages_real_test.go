package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	realPagesDir = "../../web/assets/ui"
	staticDir    = "../../web/assets/static"
	// kit-owned scripts (login.js) are served under /static/ but live in the kit
	kitAssetsDir = "../../../dopeuikit/assets/dist"
)

// TestRealPagesCompile compiles every real app page (web/assets/ui/*.dopeui) — the
// same sources servePage serves — so a broken page fails here, not at runtime.
func TestRealPagesCompile(t *testing.T) {
	pages, err := filepath.Glob(filepath.Join(realPagesDir, "*.dopeui"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(pages) != 6 {
		t.Fatalf("expected 6 app pages, found %d: %v", len(pages), pages)
	}
	for _, path := range pages {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if _, err := Compile(filepath.Base(path), src); err != nil {
			t.Errorf("compile %s: %v", filepath.Base(path), err)
		}
	}
}

// idsCreatedByJS are element ids that scripts create at runtime rather than
// find in the static page, so the selector contract does not require them.
var idsCreatedByJS = map[string]bool{"authorsDatalist": true}

// loadBearingClasses lists CSS classes JS selects on static markup. host-actions
// is on every page (the topbar emits it); the rest are board-only widgets.
var loadBearingClasses = map[string][]string{
	"":      {"host-actions"},
	"board": {"card-detail", "preview-screen-toggle"},
}

var (
	idGetRe    = regexp.MustCompile(`getElementById\("([^"]+)"\)`)
	idQueryRe  = regexp.MustCompile(`querySelector(?:All)?\("#([A-Za-z0-9_-]+)"[^"]*"?\)`)
	importReJS = regexp.MustCompile(`from\s+"\./([a-z0-9_-]+\.js)"|import\s+"\./([a-z0-9_-]+\.js)"`)
	scriptSrc  = regexp.MustCompile(`src="/static/([a-z0-9_-]+\.js)"`)
)

// TestPageSelectorContract asserts every element id and load-bearing class that
// a page's JS closure looks up on static markup exists in the compiled page.
// The closure is the page's declared scripts + menu.js, transitively following
// relative ES-module imports.
func TestPageSelectorContract(t *testing.T) {
	pages, err := filepath.Glob(filepath.Join(realPagesDir, "*.dopeui"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, path := range pages {
		name := strings.TrimSuffix(filepath.Base(path), ".dopeui")
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		html, err := Compile(filepath.Base(path), src)
		if err != nil {
			t.Fatalf("compile %s: %v", name, err)
		}
		page := string(html)

		for _, id := range wantedIDs(t, page) {
			if idsCreatedByJS[id] {
				continue
			}
			if !strings.Contains(page, `id="`+id+`"`) {
				t.Errorf("%s: JS looks up #%s but the compiled page has no such id", name, id)
			}
		}
		for _, cls := range append(loadBearingClasses[""], loadBearingClasses[name]...) {
			if !strings.Contains(page, cls) {
				t.Errorf("%s: compiled page is missing load-bearing class %q", name, cls)
			}
		}
	}
}

// wantedIDs collects the ids the page's JS closure looks up via
// getElementById / querySelector("#id").
func wantedIDs(t *testing.T, page string) []string {
	closure := jsClosure(t, entryScripts(page))
	set := map[string]bool{}
	for _, file := range closure {
		body, err := os.ReadFile(filepath.Join(staticDir, file))
		if os.IsNotExist(err) {
			body, err = os.ReadFile(filepath.Join(kitAssetsDir, file))
		}
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(body)
		for _, m := range idGetRe.FindAllStringSubmatch(text, -1) {
			set[m[1]] = true
		}
		for _, m := range idQueryRe.FindAllStringSubmatch(text, -1) {
			set[m[1]] = true
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func entryScripts(page string) []string {
	var out []string
	for _, m := range scriptSrc.FindAllStringSubmatch(page, -1) {
		out = append(out, m[1])
	}
	return out
}

// jsClosure returns the entry scripts plus every relative module they import,
// transitively.
func jsClosure(t *testing.T, entries []string) []string {
	seen := map[string]bool{}
	var order []string
	var visit func(string)
	visit = func(file string) {
		if seen[file] {
			return
		}
		seen[file] = true
		order = append(order, file)
		body, err := os.ReadFile(filepath.Join(staticDir, file))
		if os.IsNotExist(err) {
			body, err = os.ReadFile(filepath.Join(kitAssetsDir, file))
		}
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, m := range importReJS.FindAllStringSubmatch(string(body), -1) {
			dep := m[1]
			if dep == "" {
				dep = m[2]
			}
			visit(dep)
		}
	}
	for _, e := range entries {
		visit(e)
	}
	return order
}
