package inline

import "testing"

// A link is one thing, whether or not it spells out its protocol. The
// underscores in a wiki path are part of the address, not italic markers, and
// an imported source is full of links somebody pasted without the https://
// (issue #82).

func TestProtocollessURLKeepsItsUnderscores(t *testing.T) {
	const src = "Источник: en.wikipedia.org/wiki/Dawes_Road_Cemetery"
	runs := Parse4sElem(src)
	if got := plain(runs); got != src {
		t.Errorf("got %q, want %q", got, src)
	}
	for _, r := range runs {
		if r.Kind == "italic" {
			t.Errorf("the path turned italic: %#v", runs)
		}
	}
}

func TestProtocollessURLIsNotGlued(t *testing.T) {
	const src = "www.example.com/a-b_c и что-то ещё"
	want := "www.example.com/a-b_c и\u00a0что\u2011то ещё"
	if got := ReplaceNoBreak(src, NoBreak{}); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The italic markers themselves still work next to a link.
func TestItalicStillParsesBesideAURL(t *testing.T) {
	runs := Parse4sElem("_курсив_ рядом с en.wikipedia.org/wiki/A_B")
	var italic bool
	for _, r := range runs {
		if r.Kind == "italic" && r.Text == "курсив" {
			italic = true
		}
	}
	if !italic {
		t.Errorf("no italic run in %#v", runs)
	}
}
