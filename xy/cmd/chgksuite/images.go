package main

import (
	"fmt"
	"os"
	"path/filepath"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/inline"
)

// loadImages reads every picture the document's (img …) directives name, from
// the directory the source file sits in — chgksuite's search_for_imgfile. A
// name it can't find is left out: the exporters print a marker in its place.
func loadImages(doc fsource.Doc, dir string) (map[string][]byte, error) {
	images := map[string][]byte{}
	for _, name := range imageNames(doc) {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		images[name] = data
	}
	return images, nil
}

func imageNames(doc fsource.Doc) []string {
	var names []string
	seen := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch val := v.(type) {
		case string:
			for _, r := range inline.Parse4sElem(val) {
				if r.Kind != "img" {
					continue
				}
				im, ok := inline.ParseImg(r.Text)
				if !ok || seen[im.Name] {
					continue
				}
				seen[im.Name] = true
				names = append(names, im.Name)
			}
		case []any:
			for _, it := range val {
				walk(it)
			}
		case *fsource.Question:
			for _, k := range val.Keys() {
				walk(val.Get(k))
			}
		}
	}
	for _, el := range doc {
		walk(el.Content)
	}
	return names
}
