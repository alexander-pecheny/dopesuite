// Cyrillic lint (root docs/adr/0006): user-facing Russian belongs in a
// Catalog, so no .go, .ts or .dopeui source may carry any. What still does is
// listed in allowlist.txt, which is the migration's burn-down — a file that no
// longer has Cyrillic must leave the list, or the lint fails.
//
//	go -C scripts/cyrillic run . [root...]
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

var roots = []string{"dopecore", "dopeuikit", "xy", "dope", "scripts"}

// Skipped wholesale: generated files carry what a Catalog put there, tests
// name a string to assert on it, the chgksuite parity labels mirror upstream,
// and towns.ts is data (Russian town names), not copy.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "testdata": true, "jstest": true,
}

func skipPath(rel string) bool {
	return strings.HasPrefix(rel, "xy/internal/chgk/i18n/assets/") || rel == "xy/web/ts/towns.ts"
}

func skipFile(name string) bool {
	switch {
	case strings.HasSuffix(name, "_gen.go"), strings.HasSuffix(name, "_test.go"):
		return true
	case strings.HasSuffix(name, "_gen.ts"), strings.HasSuffix(name, ".test.ts"):
		return true
	}
	return false
}

func linted(name string) bool {
	ext := filepath.Ext(name)
	return ext == ".go" || ext == ".ts" || ext == ".dopeui"
}

func main() {
	if _, err := os.Stat("dopecore/i18nstrings"); err != nil {
		if err := os.Chdir("../.."); err != nil {
			fail(err)
		}
		if _, err := os.Stat("dopecore/i18nstrings"); err != nil {
			fail(fmt.Errorf("run from the repo root or via `go -C scripts/cyrillic run .`"))
		}
	}
	if args := os.Args[1:]; len(args) > 0 {
		roots = args
	}
	allowed, err := readAllowlist("scripts/cyrillic/allowlist.txt")
	if err != nil {
		fail(err)
	}
	offenders, err := walk()
	if err != nil {
		fail(err)
	}

	var bad []string
	for _, o := range offenders {
		if !allowed[o.path] {
			bad = append(bad, fmt.Sprintf("%s:%d: Cyrillic outside a Catalog: %s", o.path, o.line, o.text))
		}
		delete(allowed, o.path)
	}
	for path := range allowed {
		if under(path) {
			bad = append(bad, path+": allowlisted but has no Cyrillic any more — drop it from scripts/cyrillic/allowlist.txt")
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		fmt.Fprintln(os.Stderr, strings.Join(bad, "\n"))
		os.Exit(1)
	}
}

// under reports whether a path is in this run's roots, so narrowing the roots
// cannot make the rest of the allowlist look stale.
func under(path string) bool {
	for _, root := range roots {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

type hit struct {
	path string
	line int
	text string
}

func walk() ([]hit, error) {
	var out []hit
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel := filepath.ToSlash(path)
			if d.IsDir() {
				if skipDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !linted(d.Name()) || skipFile(d.Name()) || skipPath(rel) {
				return nil
			}
			h, err := scan(rel)
			if err != nil {
				return err
			}
			if h != nil {
				out = append(out, *h)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

// scan returns the first Cyrillic line of a file, which is all a burn-down
// list needs: the file is either on it or it is not.
func scan(path string) (*hit, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 1; s.Scan(); n++ {
		line := s.Text()
		if strings.IndexFunc(line, isCyrillic) >= 0 {
			return &hit{path: path, line: n, text: strings.TrimSpace(line)}, nil
		}
	}
	return nil, s.Err()
}

func isCyrillic(r rune) bool { return unicode.Is(unicode.Cyrillic, r) }

func readAllowlist(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line, _, _ = strings.Cut(line, "#"); strings.TrimSpace(line) != "" {
			out[strings.TrimSpace(line)] = true
		}
	}
	return out, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "cyrillic: "+err.Error())
	os.Exit(1)
}
