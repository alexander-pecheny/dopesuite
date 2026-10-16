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
		if res.Images, err = imagesBeside(filepath.Dir(path), res.Source); err != nil {
			return nil, err
		}
	}
	res.SafeImageNames()
	return res, nil
}

// imagesBeside reads the pictures source names from dir. A missing one is
// left out, as a zip without it would be: the card keeps its directive.
func imagesBeside(dir, source string) ([]Image, error) {
	var images []Image
	for _, name := range listexport.ImageRefs(source) {
		mime, ok := imageExts[strings.ToLower(filepath.Ext(name))]
		if !ok || name != filepath.Base(name) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		images = append(images, Image{Name: name, MIME: mime, Data: data})
	}
	return images, nil
}
