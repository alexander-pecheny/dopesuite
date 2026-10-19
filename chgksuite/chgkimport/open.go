package chgkimport

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/listexport"
)

// Open imports the file at path. A .4s carries no pictures of its own, so the
// ones its (img …) directives name are read from the folder it sits in.
func Open(path string) (*Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	res, err := Parse(filepath.Base(path), data, "chgk")
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Ext(path), ".4s") {
		images, err := ImagesBeside(filepath.Dir(path), res.Source)
		if err != nil {
			return nil, err
		}
		res.AddImages(images)
	}
	res.SafeImageNames()
	return res, nil
}

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
