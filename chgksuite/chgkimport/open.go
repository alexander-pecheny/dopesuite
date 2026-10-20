package chgkimport

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/listexport"
)

// ImagesBeside reads the pictures source names from dir, each by its base name,
// as a zip would hold them. A missing one is left out: the card keeps its
// directive, and the import reports it.
func ImagesBeside(dir, source string) ([]Image, error) {
	var images []Image
	for _, ref := range listexport.ImageRefs(source) {
		name := filepath.Base(ref)
		mimeType, ok := imageExts[strings.ToLower(filepath.Ext(name))]
		if !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		images = append(images, Image{Name: name, MIME: mimeType, Data: data})
	}
	return images, nil
}
