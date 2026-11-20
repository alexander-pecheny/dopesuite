package typstwasm_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/handout"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/xyimage"
)

// An Edited Image reaches typst as its vector rendition: the picture embedded,
// the overlay's text set as real text in Noto Sans (xy ADR-0024).
func TestEditedImageSetsTextAsText(t *testing.T) {
	raw, err := os.ReadFile("../xyimage/testdata/fixture.xyimage")
	if err != nil {
		t.Fatal(err)
	}
	svg, _, err := xyimage.Vector(raw, nil)
	if err != nil || svg == nil {
		t.Fatalf("the fixture has no vector rendition: %v", err)
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

// An uploaded Face is set under the family name the image gives it, for one
// generation only (xy ADR-0025): here Noto Sans Bold stands in for an upload,
// under a name no bundled font has.
func TestNamedFontLastsOneGeneration(t *testing.T) {
	bold, ok := handout.BundledFontFile("NotoSans-Bold.ttf")
	if !ok {
		t.Fatal("no bundled bold")
	}
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="40mm" height="10mm" viewBox="0 0 40 10"><text x="1" y="7" font-family="xyf-test" font-size="5">Upload</text></svg>`)
	const typ = `#set page(width: 60mm, height: 20mm, margin: 5mm)
#image("t.svg", width: 40mm)`
	p := pool(t, 1)
	ctx := context.Background()
	compile := func(files map[string][]byte) []byte {
		t.Helper()
		if err := p.SetImages(ctx, files); err != nil {
			t.Fatal(err)
		}
		pdf, _, err := p.Compile(ctx, typ, true)
		if err != nil {
			t.Fatal(err)
		}
		return pdf
	}
	pdf := compile(map[string][]byte{"t.svg": svg, xyimage.FontKeyPrefix + "xyf-test": bold})
	if !bytes.Contains(pdf, []byte("NotoSans-Bold")) {
		t.Fatal("the text was not set in the face named xyf-test")
	}
	pdf = compile(map[string][]byte{"t.svg": svg})
	if bytes.Contains(pdf, []byte("NotoSans-Bold")) {
		t.Fatal("the face outlived its generation")
	}
}
