package handout

import (
	"testing"

	"golang.org/x/image/font/sfnt"
)

// The symbols scripts/symbolfonts.py transplants into the bundled faces from
// Noto Sans Symbols 2. Stock Noto Sans has none of them, so an author's ⏸ used
// to come out of the PDF export as tofu — typst only sees these embedded
// fonts. If this fails after a font rebuild, rerun the script.
var transplantedSymbols = []rune("⏸⏹⏺⏯⏩⏪⏭⏮▶◀✓✔✖✗⌚⌛⏱⏲⏳")

func TestBundledFontsCoverTransplantedSymbols(t *testing.T) {
	fonts, err := BundledFonts()
	if err != nil {
		t.Fatalf("fonts: %v", err)
	}
	var buf sfnt.Buffer
	for i, data := range fonts {
		f, err := sfnt.Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", fontNames[i], err)
		}
		for _, r := range transplantedSymbols {
			gi, err := f.GlyphIndex(&buf, r)
			if err != nil {
				t.Fatalf("%s %q: %v", fontNames[i], r, err)
			}
			if gi == 0 {
				t.Errorf("%s: no glyph for %q (%U)", fontNames[i], r, r)
			}
		}
	}
}
