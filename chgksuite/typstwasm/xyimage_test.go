package typstwasm_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/imgconv"
)

// An Edited Image reaches typst as its vector rendition: the picture embedded,
// the overlay's text set as real text in Noto Sans (xy ADR-0024).
func TestEditedImageSetsTextAsText(t *testing.T) {
	raw, err := os.ReadFile("../xyimage/testdata/fixture.xyimage")
	if err != nil {
		t.Fatal(err)
	}
	svg, ok := imgconv.Vector(raw)
	if !ok {
		t.Fatal("the fixture has no vector rendition")
	}
	p := pool(t, 1)
	ctx := context.Background()
	if err := p.SetImages(ctx, map[string][]byte{"plakat.xyimage": svg}); err != nil {
		t.Fatal(err)
	}
	pdf, _, err := p.Compile(ctx, `#set page(width: 60mm, height: 40mm, margin: 5mm)
#image("plakat.xyimage", format: "svg", width: 40mm)`, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(pdf, []byte("NotoSans")) || !bytes.Contains(pdf, []byte("/ToUnicode")) {
		t.Fatal("the overlay's text did not reach the PDF as text in Noto Sans")
	}
	if !bytes.Contains(pdf, []byte("/Subtype /Image")) && !bytes.Contains(pdf, []byte("/Subtype/Image")) {
		t.Fatal("the picture under the text did not reach the PDF")
	}
}
