package fallbackfont_test

import (
	"testing"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/fallbackfont"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/handout"
)

func set(t *testing.T) *fallbackfont.Set {
	t.Helper()
	fonts, err := handout.BundledFonts()
	if err != nil {
		t.Fatal(err)
	}
	return fallbackfont.NewSet(fonts)
}

// One character per script scripts/fallbackfonts.py brings in. typst sees no
// font but the bundled ones, so a script none of them covers is tofu in the
// PDF export and the handouts: an Armenian word in a comment and a Korean
// namu.wiki link came out as boxes. If this fails after a font rebuild, rerun
// that script.
func TestEveryScriptHasAFamily(t *testing.T) {
	s := set(t)
	for _, r := range "օაשعकকதกሀ♈♔∮中あ한" {
		if len(s.For(string(r))) != 1 {
			t.Errorf("%q (%U): %d families, want 1", r, r, len(s.For(string(r))))
		}
	}
}

// A package Noto Sans covers whole loads nothing, which is the point: the
// pool holds a family only while a source needs it.
func TestNotoSansTextNeedsNone(t *testing.T) {
	s := set(t)
	if got := s.For("Вопрос 1. «Ёлка» — naïve café, ½ × 2 ± 1 ⏸"); len(got) != 0 {
		t.Errorf("got %d families for text Noto Sans has", len(got))
	}
}

// A family comes with all its faces under one name, so typst picks among them
// by weight.
func TestFamilyCarriesItsWeights(t *testing.T) {
	got := set(t).For("օտար օտար 中")
	if len(got) != 2 {
		t.Fatalf("got %d families, want Armenian and SC", len(got))
	}
	if got[0].Name != "Noto Sans Armenian" || len(got[0].Faces) != 2 {
		t.Errorf("Armenian: %q with %d faces, want Regular and Bold", got[0].Name, len(got[0].Faces))
	}
	if got[1].Name != "Noto Sans SC" || len(got[1].Faces) != 1 {
		t.Errorf("SC: %q with %d faces", got[1].Name, len(got[1].Faces))
	}
}
