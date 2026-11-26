package handout

import (
	"testing"

	"golang.org/x/image/font/sfnt"
)

// The symbols scripts/handoutfonts.py transplants into the bundled faces from
// Noto Sans Symbols 2. Stock Noto Sans has none of them, so an author's ⏸ used
// to come out of the PDF export as tofu — typst only sees these embedded
// fonts. If this fails after a font rebuild, rerun that script.
var transplantedSymbols = []rune("⏸⏹⏺⏯⏩⏪⏭⏮▶◀✓✔✖✗⌚⌛⏱⏲⏳")

func TestBundledFontsCoverTransplantedSymbols(t *testing.T) {
	fonts, err := BundledFonts()
	if err != nil {
		t.Fatalf("fonts: %v", err)
	}
	var buf sfnt.Buffer
	for i, data := range fonts[:len(fontNames)] {
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

// One character per script scripts/fallbackfonts.py brings in. typst sees no
// font but the bundled ones, so a script none of them covers is tofu in the
// PDF export and the handouts: an Armenian word in a comment came out as
// boxes. If this fails after a font rebuild, rerun that script.
var fallbackProbes = []rune("օაשعकকதกሀ♈♔∮中あ한")

func TestBundledFontsCoverOtherScripts(t *testing.T) {
	fonts, err := BundledFonts()
	if err != nil {
		t.Fatalf("fonts: %v", err)
	}
	var buf sfnt.Buffer
	parsed := make([]*sfnt.Font, len(fonts))
	for i, data := range fonts {
		if parsed[i], err = sfnt.Parse(data); err != nil {
			t.Fatalf("font %d: %v", i, err)
		}
	}
	for _, r := range fallbackProbes {
		found := false
		for _, f := range parsed {
			if gi, err := f.GlyphIndex(&buf, r); err == nil && gi != 0 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no bundled face has %q (%U)", r, r)
		}
	}
}
