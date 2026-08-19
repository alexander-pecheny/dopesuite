// Package uitest holds the test both apps run over their real pages: every
// .dopeui page compiles, and every id and load-bearing markup a page's scripts
// look up on static markup exists in the compiled page.
package uitest

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// PageContract is one app's page set and the allowances its scripts need:
// Pages is the count PagesDir must hold (fewer means a moved directory, not a
// pass); IDsCreatedByJS are ids scripts build at runtime; LoadBearing maps a
// page name ("" = every page) to markup substrings its scripts bind to.
type PageContract struct {
	Compile             func(name string, src []byte) ([]byte, error)
	PagesDir, StaticDir string
	Pages               int
	IDsCreatedByJS      map[string]bool
	LoadBearing         map[string][]string
}

var (
	idGetRe = regexp.MustCompile(`getElementById\("([^"]+)"\)`)
	// modal("stem") and wireModal("stem", openBtnId) (xy) hand ids in as string
	// arguments idGetRe never sees; a stem names the <stem>Overlay element.
	modalRe     = regexp.MustCompile(`\bmodal\("([A-Za-z0-9_-]+)"`)
	wireModalRe = regexp.MustCompile(`wireModal\("([A-Za-z0-9_-]+)",\s*"([A-Za-z0-9_-]+)"`)
	idQueryRe   = regexp.MustCompile(`querySelector(?:All)?\("#([A-Za-z0-9_-]+)"[^"]*"?\)`)
	importReJS  = regexp.MustCompile(`from\s+"\./([a-z0-9_-]+\.js)"|import\s+"\./([a-z0-9_-]+\.js)"`)
	scriptSrc   = regexp.MustCompile(`src="/static/((?:dist/)?[a-z0-9_-]+\.js)"`)
)

// kitDist holds the kit's login.js/menu.js, served under /static/ but living here.
var kitDist = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "assets", "dist")
}()

func (c PageContract) Run(t *testing.T) {
	t.Helper()
	pages, err := filepath.Glob(filepath.Join(c.PagesDir, "*.dopeui"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(pages) != c.Pages {
		t.Fatalf("expected %d pages in %s, found %d: %v", c.Pages, c.PagesDir, len(pages), pages)
	}
	for _, path := range pages {
		name := strings.TrimSuffix(filepath.Base(path), ".dopeui")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			html, err := c.Compile(filepath.Base(path), src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			page := string(html)
			for _, id := range c.wantedIDs(t, page) {
				if !c.IDsCreatedByJS[id] && !strings.Contains(page, `id="`+id+`"`) {
					t.Errorf("JS looks up #%s but the compiled page has no such id", id)
				}
			}
			for _, sub := range append(c.LoadBearing[""], c.LoadBearing[name]...) {
				if !strings.Contains(page, sub) {
					t.Errorf("compiled page is missing load-bearing markup %q", sub)
				}
			}
		})
	}
}

func (c PageContract) wantedIDs(t *testing.T, page string) []string {
	set := map[string]bool{}
	for _, file := range c.closure(t, scriptSrc.FindAllStringSubmatch(page, -1)) {
		text := string(c.read(t, file))
		for _, m := range idGetRe.FindAllStringSubmatch(text, -1) {
			set[m[1]] = true
		}
		for _, m := range idQueryRe.FindAllStringSubmatch(text, -1) {
			set[m[1]] = true
		}
		for _, m := range modalRe.FindAllStringSubmatch(text, -1) {
			set[m[1]+"Overlay"] = true
		}
		for _, m := range wireModalRe.FindAllStringSubmatch(text, -1) {
			set[m[1]+"Overlay"] = true
			set[m[2]] = true
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// closure follows relative imports transitively; each resolves against the
// importer's directory, so `from "./app.js"` in dist/profile.js is dist/app.js.
func (c PageContract) closure(t *testing.T, entries [][]string) []string {
	seen := map[string]bool{}
	var order []string
	var visit func(string)
	visit = func(file string) {
		if seen[file] {
			return
		}
		seen[file] = true
		order = append(order, file)
		for _, m := range importReJS.FindAllStringSubmatch(string(c.read(t, file)), -1) {
			visit(filepath.Join(filepath.Dir(file), m[1]+m[2]))
		}
	}
	for _, m := range entries {
		visit(m[1])
	}
	return order
}

func (c PageContract) read(t *testing.T, file string) []byte {
	body, err := os.ReadFile(filepath.Join(c.StaticDir, file))
	if os.IsNotExist(err) {
		body, err = os.ReadFile(filepath.Join(kitDist, file))
	}
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	return body
}
