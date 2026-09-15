package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The kit's PageContract reads the BUILT bundles, where a DOM helper's name has
// been minified away — so `byId("txTitle")` is invisible to it and a page that
// lost an id passes clean. (It did: `titleid` on a topbar that also carries
// crumbs emits nothing at all, and two pages looked up a heading that was never
// there.)
//
// This reads the SOURCES instead, follows each page's import closure, and
// insists that every id a script asks for exists in the page that loads it.

var (
	scriptAttr = regexp.MustCompile(`scripts="dist/([a-z0-9_-]+)\.js"`)
	idLookup   = regexp.MustCompile(`\b(?:byId|maybe)(?:<[^>(]*>)?\("([^"]+)"\)`)
	tsImport   = regexp.MustCompile(`(?:from|import) "(\.\.?/[a-z0-9_/-]+?)(?:\.js)?"`)
	emittedID  = regexp.MustCompile(`id="([^"]+)"`)
)

func TestEveryIdAScriptAsksForIsOnItsPage(t *testing.T) {
	pages, err := filepath.Glob(filepath.Join("../assets/ui", "*.dopeui"))
	if err != nil || len(pages) == 0 {
		t.Fatalf("no pages found: %v", err)
	}
	for _, path := range pages {
		name := strings.TrimSuffix(filepath.Base(path), ".dopeui")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			entry := scriptAttr.FindSubmatch(src)
			if entry == nil {
				t.Skip("the page loads no script of its own")
			}
			html, err := Compile(path, src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			present := map[string]bool{}
			for _, m := range emittedID.FindAllSubmatch(html, -1) {
				present[string(m[1])] = true
			}
			var missing []string
			for _, id := range lookedUp(t, string(entry[1])) {
				if !present[id] {
					missing = append(missing, id)
				}
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				t.Errorf("the script looks up ids the page does not carry: %s", strings.Join(missing, ", "))
			}
		})
	}
}

// lookedUp is every id the page's entry module and everything it imports ask
// for, following `./name` imports from web/ts/pages/<entry>.ts outward.
func lookedUp(t *testing.T, entry string) []string {
	t.Helper()
	seen := map[string]bool{}
	ids := map[string]bool{}
	var walk func(path string)
	walk = func(path string) {
		path = filepath.Clean(path)
		if seen[path] {
			return
		}
		seen[path] = true
		body, err := os.ReadFile(path)
		if err != nil {
			return // a generated or absent module is not this test's business
		}
		for _, m := range idLookup.FindAllStringSubmatch(string(body), -1) {
			ids[m[1]] = true
		}
		for _, m := range tsImport.FindAllStringSubmatch(string(body), -1) {
			walk(filepath.Join(filepath.Dir(path), m[1]+".ts"))
		}
	}
	walk(filepath.Join("../ts/pages", entry+".ts"))

	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	if len(out) == 0 {
		t.Fatalf("no id lookups found for %s — the walk found no sources", entry)
	}
	return out
}
