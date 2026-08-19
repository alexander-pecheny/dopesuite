// Package uitest holds the test the two apps run over their real pages: a
// PageContract says every .dopeui page compiles, and every element id and
// load-bearing markup that a page's scripts look up on static markup exists in
// the compiled page. The regexes, the dist/ handling and the modal("stem")
// knowledge live here once; each app keeps its allow-lists.
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

// PageContract is one app's page set and the allowances its scripts need.
type PageContract struct {
	// Compile is the app's ui.Compile, carrying its vocabulary overlay.
	Compile func(name string, src []byte) ([]byte, error)
	// PagesDir holds the app's *.dopeui sources; StaticDir the served static
	// tree (page scripts under it, built ESM under its dist/).
	PagesDir, StaticDir string
	// Pages is how many pages PagesDir must hold — a glob that finds fewer is a
	// moved directory, not a passing test.
	Pages int
	// IDsCreatedByJS are ids scripts create at runtime rather than find in the
	// static page, so the contract does not require them.
	IDsCreatedByJS map[string]bool
	// LoadBearing maps a page name ("" = every page) to markup substrings its
	// scripts bind to — classes, tags — that must survive in the compiled page.
	LoadBearing map[string][]string
}

var (
	idGetRe = regexp.MustCompile(`getElementById\("([^"]+)"\)`)
	// modal("stem") (xy's modal.ts) and wireModal("stem", openBtnId) hand their
	// ids in as STRING ARGUMENTS, so idGetRe never sees them — a whole family of
	// /profile buttons was invisible to this contract until a missing one bricked
	// the page at module load. A stem names the <stem>Overlay element.
	modalRe     = regexp.MustCompile(`\bmodal\("([A-Za-z0-9_-]+)"`)
	wireModalRe = regexp.MustCompile(`wireModal\("([A-Za-z0-9_-]+)",\s*"([A-Za-z0-9_-]+)"`)
	idQueryRe   = regexp.MustCompile(`querySelector(?:All)?\("#([A-Za-z0-9_-]+)"[^"]*"?\)`)
	importReJS  = regexp.MustCompile(`from\s+"\./([a-z0-9_-]+\.js)"|import\s+"\./([a-z0-9_-]+\.js)"`)
	// The built ESM lives under /static/dist/; without the optional segment the
	// contract silently covers only the kit's /static/menu.js.
	scriptSrc = regexp.MustCompile(`src="/static/((?:dist/)?[a-z0-9_-]+\.js)"`)
)

// kitDist is the kit's built scripts (login.js, menu.js), served by the apps
// under /static/ but living here — found from this file, whatever the app's depth.
var kitDist = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "assets", "dist")
}()

// Run compiles every page and checks the selector contract of each.
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

// wantedIDs collects the ids the page's script closure looks up.
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

// closure is the page's entry scripts plus every relative module they import,
// transitively. A relative import resolves against the IMPORTER's directory:
// `from "./app.js"` inside dist/profile.js means dist/app.js.
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
