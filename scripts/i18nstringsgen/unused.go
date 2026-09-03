package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A string nobody reads is worse than no string: it looks like copy under
// review and translates at cost. checkUnused fails when the module's tree holds
// no reference to a generated id — the Go path (Board.Delete.Confirm), the
// TypeScript one (board.delete.confirm) or `@board.delete.confirm` in a page.
func checkUnused(dir, ts string, ref *catalog) error {
	module := filepath.Dir(dir)
	roots := []string{module}
	if ts != "" && !strings.HasPrefix(filepath.Clean(ts)+string(filepath.Separator), filepath.Clean(module)+string(filepath.Separator)) {
		roots = append(roots, ts)
	}
	paths := map[string]map[string]string{".go": {}, ".ts": {}, ".dopeui": {}}
	for _, id := range ref.ids() {
		parts := strings.Split(id, ".")
		paths[".go"][join(parts, pascal)] = id
		paths[".ts"][join(parts, camel)] = id
		paths[".dopeui"]["@"+id] = id
	}
	used, err := scanRefs(roots, paths)
	if err != nil {
		return err
	}
	var unused []string
	for _, id := range ref.ids() {
		if !used[id] {
			unused = append(unused, id)
		}
	}
	if len(unused) > 0 {
		return fmt.Errorf("%s: nothing under %s reads these strings — use them or delete them:\n\t%s",
			dir, strings.Join(roots, ", "), strings.Join(unused, "\n\t"))
	}
	return nil
}

func join(parts []string, conv func(string) string) string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = conv(p)
	}
	return strings.Join(out, ".")
}

var (
	dottedRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+`)
	dopeuiRe = regexp.MustCompile(`@[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)+`)
	skipDirs = map[string]bool{".git": true, "node_modules": true, "dist": true, "testdata": true, "jstest": true}
)

// scanRefs walks the roots and reports which ids a source file names. A dotted
// path counts for the LONGEST id it ends with, so `s.Board.Delete.Confirm`
// reads board.delete.confirm and nothing else — a shorter id that happens to
// share the tail stays unused. Tests do not count: a string only a test reads
// is still a string nobody shows.
func scanRefs(roots []string, paths map[string]map[string]string) (map[string]bool, error) {
	used := map[string]bool{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if skipDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(path)
			if paths[ext] == nil || generated(path, ext) || isTest(path, ext) {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			re := dottedRe
			if ext == ".dopeui" {
				re = dopeuiRe
			}
			for _, m := range re.FindAllString(string(body), -1) {
				if id, ok := longestID(m, paths[ext]); ok {
					used[id] = true
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return used, nil
}

// longestID returns the id whose path is the longest tail of m.
func longestID(m string, paths map[string]string) (string, bool) {
	for parts := strings.Split(m, "."); len(parts) > 0; parts = parts[1:] {
		if id, ok := paths[strings.Join(parts, ".")]; ok {
			return id, true
		}
	}
	return "", false
}

func generated(path, ext string) bool { return strings.HasSuffix(path, "_gen"+ext) }

func isTest(path, ext string) bool {
	return strings.HasSuffix(path, "_test"+ext) || strings.HasSuffix(path, ".test"+ext)
}
