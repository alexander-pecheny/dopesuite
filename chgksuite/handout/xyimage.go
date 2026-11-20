package handout

import (
	"maps"
	"strings"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/xyimage"
)

// editedImageExt is the extension of xy's Edited Image (xy ADR-0024).
const editedImageExt = ".xyimage"

func isEditedImageName(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), editedImageExt)
}

// fontZips is the Font Family zips among the files an export was handed
// (xy ADR-0025). They are set aside before the pictures are narrowed to the
// ones the handouts name, which would drop them.
func fontZips(images map[string][]byte) map[string][]byte {
	out := map[string][]byte{}
	for name, data := range images {
		if xyimage.IsFontZipKey(name) {
			out[name] = data
		}
	}
	return out
}

// vectorImages is the images as typst gets them: an Edited Image as its
// vector rendition, one SVG with its text kept as text, plus the uploaded
// Faces it is set in, found in zips; anything else as it is. The name stays,
// and GenerateTyp tells typst the format.
func vectorImages(images, zips map[string][]byte) (map[string][]byte, error) {
	out := make(map[string][]byte, len(images))
	for name, data := range images {
		if xyimage.IsFontZipKey(name) {
			continue
		}
		out[name] = data
		svg, faces, err := xyimage.Vector(data, zips)
		if err != nil {
			return nil, xyimage.UserError(name, err)
		}
		if svg != nil {
			out[name] = svg
			maps.Copy(out, faces)
		}
	}
	return out, nil
}
