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
	realPagesDir = "../assets/ui"
	staticDir    = "../assets/static"
)

// TestRealPagesCompile compiles every real app page (web/assets/ui/*.dopeui) — the
// same sources the server serves — so a broken page fails here, not at runtime.
func TestRealPagesCompile(t *testing.T) {
	pages, err := filepath.Glob(filepath.Join(realPagesDir, "*.dopeui"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(pages) != 5 {
		t.Fatalf("expected 5 app pages, found %d: %v", len(pages), pages)
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

// idsCreatedByJS are element ids scripts create at runtime rather than find in
// the static page, so the selector contract does not require them. (None yet —
// the dope game DOM is JS-built, but no static-markup lookup targets a built id.)
var idsCreatedByJS = map[string]bool{}

// loadBearingClasses / substrings lists structural markup the page scripts bind
// to (see DOPE-INVENTORY §JS contract). host-actions is on every page (topbar);
// the game pages measure .sheet-frame and mount into .table-host; login and the
// viewer set the title on `.host-top h1`.
var loadBearingSubstrings = map[string][]string{
	"":       {"host-actions"},
	"login":  {"host-top", "<h1>"},
	"host":   {"game-host-top", "sheet-frame", "table-host"},
	"viewer": {"game-host-top", "sheet-frame", "table-host", "<h1>"},
	"od":     {"game-host-top", "sheet-frame", "table-host", "od-header-progress"},
	"si":     {"game-host-top", "sheet-frame", "table-host"},
}

var (
	idGetRe   = regexp.MustCompile(`getElementById\("([^"]+)"\)`)
	idQueryRe = regexp.MustCompile(`querySelector(?:All)?\("#([A-Za-z0-9_-]+)"`)
	scriptSrc = regexp.MustCompile(`src="/static/([a-z0-9_-]+\.js)"`)
)

// TestPageSelectorContract asserts every element id and load-bearing markup that
// a page's scripts look up on static markup exists in the compiled page. The
// closure is the page's declared scripts plus the menu.js boot script (dope's
// page scripts are classic, so there are no module imports to follow).
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
		for _, sub := range append(loadBearingSubstrings[""], loadBearingSubstrings[name]...) {
			if !strings.Contains(page, sub) {
				t.Errorf("%s: compiled page is missing load-bearing markup %q", name, sub)
			}
		}
	}
}

// wantedIDs collects the ids the page's scripts look up via getElementById /
// querySelector("#id").
func wantedIDs(t *testing.T, page string) []string {
	set := map[string]bool{}
	for _, file := range entryScripts(page) {
		body, err := os.ReadFile(filepath.Join(staticDir, file))
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
