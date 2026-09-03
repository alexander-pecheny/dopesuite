// Catalog codegen (root docs/adr/0006): reads a module's
// <dir>/<lang>/*.toml and writes the typed Strings — Go beside the catalog,
// TypeScript into the module's TS tree. One binary for every module; paths are
// repo-root-relative because `go -C scripts/i18nstringsgen run .` starts here.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// defaultLang is the reference: every other language must define exactly its
// ids, with the same parameters.
const defaultLang = "ru"

func main() {
	dir := flag.String("dir", "", "catalog and Go output directory, relative to the repo root")
	ts := flag.String("ts", "", "TypeScript output directory, relative to the repo root")
	unused := flag.Bool("unused", true, "fail when the module's tree references none of a generated id")
	flag.Parse()
	if *dir == "" {
		fatal("-dir is required")
	}
	if _, err := os.Stat("dopecore/i18nstrings"); err != nil {
		if err := os.Chdir("../.."); err != nil {
			fatal(err.Error())
		}
		if _, err := os.Stat("dopecore/i18nstrings"); err != nil {
			fatal("run from the repo root or via `go -C scripts/i18nstringsgen run .`")
		}
	}
	if err := generate(*dir, *ts, *unused); err != nil {
		fatal(err.Error())
	}
}

func generate(dir, ts string, unused bool) error {
	langs, err := languages(dir)
	if err != nil {
		return err
	}
	cats := make([]*catalog, 0, len(langs))
	for _, lang := range langs {
		c, err := readCatalog(dir, lang)
		if err != nil {
			return err
		}
		cats = append(cats, c)
	}
	ref := cats[0] // languages() puts defaultLang first
	for _, c := range cats[1:] {
		if err := c.agrees(ref); err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
	}

	types, err := emitGoTypes(ref)
	if err != nil {
		return err
	}
	files := map[string][]byte{filepath.Join(dir, "types_gen.go"): types}
	for _, c := range cats {
		out, err := emitGoCatalog(c)
		if err != nil {
			return err
		}
		files[filepath.Join(dir, c.lang+"_gen.go")] = out
	}
	if ts != "" {
		files[filepath.Join(ts, "i18nstrings_plural_gen.ts")] = emitTSPlural(langs)
		files[filepath.Join(ts, "i18nstrings_types_gen.ts")] = emitTSTypes(ref)
		for _, c := range cats {
			files[filepath.Join(ts, "i18nstrings_"+c.lang+"_gen.ts")] = emitTSCatalog(c)
		}
	}
	for path, body := range files {
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return err
		}
	}
	if unused {
		return checkUnused(dir, ts, ref)
	}
	return nil
}

// languages lists the catalog's language directories, the default one first.
func languages(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && ident.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i] == defaultLang) != (out[j] == defaultLang) {
			return out[i] == defaultLang
		}
		return out[i] < out[j]
	})
	if len(out) == 0 || out[0] != defaultLang {
		return nil, fmt.Errorf("%s: no %s/ catalog", dir, defaultLang)
	}
	return out, nil
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "i18nstringsgen: "+msg)
	os.Exit(1)
}
