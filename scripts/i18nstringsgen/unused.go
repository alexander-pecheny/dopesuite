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
	seen, err := scanRefs(roots)
	if err != nil {
		return err
	}
	var unused []string
	for _, id := range ref.ids() {
		parts := strings.Split(id, ".")
		if seen[".go"][join(parts, pascal)] || seen[".ts"][join(parts, camel)] || seen[".dopeui"]["@"+id] {
			continue
		}
		unused = append(unused, id)
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
	skipDirs = map[string]bool{".git": true, "node_modules": true, "dist": true, "testdata": true}
)

// scanRefs collects, per extension, every dotted path a source file mentions —
// each suffix of it too, since a call site reads `s.Board.Delete.Confirm`.
func scanRefs(roots []string) (map[string]map[string]bool, error) {
	seen := map[string]map[string]bool{".go": {}, ".ts": {}, ".dopeui": {}}
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
			if seen[ext] == nil || strings.HasSuffix(path, "_gen"+ext) {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if ext == ".dopeui" {
				for _, m := range dopeuiRe.FindAllString(string(body), -1) {
					seen[ext][m] = true
				}
				return nil
			}
			for _, m := range dottedRe.FindAllString(string(body), -1) {
				for parts := strings.Split(m, "."); len(parts) > 1; parts = parts[1:] {
					seen[ext][strings.Join(parts, ".")] = true
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return seen, nil
}
