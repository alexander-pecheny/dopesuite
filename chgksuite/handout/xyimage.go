package handout

import (
	"strings"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/imgconv"
)

// editedImageExt is the extension of xy's Edited Image (xy ADR-0024).
const editedImageExt = ".xyimage"

func isEditedImageName(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), editedImageExt)
}

// vectorImages is the images as typst gets them: an Edited Image as its
// vector rendition, one SVG with its text kept as text; anything else as it
// is. The name stays, and GenerateTyp tells typst the format.
func vectorImages(images map[string][]byte) map[string][]byte {
	var out map[string][]byte
	for name, data := range images {
		svg, ok := imgconv.Vector(data)
		if !ok {
			continue
		}
		if out == nil {
			out = make(map[string][]byte, len(images))
			for n, d := range images {
				out[n] = d
			}
		}
		out[name] = svg
	}
	if out == nil {
		return images
	}
	return out
}
