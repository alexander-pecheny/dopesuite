package docread

import (
	"bytes"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strings"
	"testing"
)

// Word's own files: inline pictures through their PICF in the Data stream,
// floating ones through the drawing's BStore; each an (img …) where its anchor
// stands, the bytes a picture that decodes.
func TestToTextImages(t *testing.T) {
	for _, c := range []struct {
		file  string
		names []string
	}{
		{"two_images.doc", []string{"p_x_image1.jpg", "p_x_image2.png"}},
		{"pictures_escher.doc", []string{"p_x_image1.png", "p_x_image2.png"}},
	} {
		data, err := os.ReadFile("testdata/" + c.file)
		if err != nil {
			t.Fatal(err)
		}
		text, images, err := ToTextImages(data, "p_x_")
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		if len(images) != len(c.names) {
			t.Fatalf("%s: %d images, want %d", c.file, len(images), len(c.names))
		}
		at := -1
		for i, img := range images {
			if img.Name != c.names[i] {
				t.Errorf("%s: image %d named %q, want %q", c.file, i, img.Name, c.names[i])
			}
			if _, _, err := image.Decode(bytes.NewReader(img.Data)); err != nil {
				t.Errorf("%s: %s does not decode: %v", c.file, img.Name, err)
			}
			// In order, and with the name's underscores as written.
			next := strings.Index(text, "(img "+img.Name+")")
			if next <= at {
				t.Errorf("%s: (img %s) at %d, after %d\n%s", c.file, img.Name, next, at, text)
			}
			at = next
		}
		plain, err := ToText(data)
		if err != nil || strings.Contains(plain, "(img") || strings.Contains(plain, pictureMark) {
			t.Errorf("%s: ToText %q, %v", c.file, plain, err)
		}
	}
}
