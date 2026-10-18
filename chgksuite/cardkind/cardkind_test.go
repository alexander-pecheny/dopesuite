package cardkind

import (
	"testing"
)

// Every kind against every question the table answers, spelled out, so a
// change to a rule shows up here as a changed row. xy/jstest/cardkind.test.js
// asks the browser's copy the same questions.
func TestKindTable(t *testing.T) {
	type row struct {
		kind                                    string
		numbered, exported, handouts, versioned bool
		marker                                  string
		section, setsBase                       bool
		game                                    string
		pickable                                bool
	}
	want := []row{
		{Question, true, true, true, true, "", false, false, "", true},
		{Theme, true, true, false, false, "", false, false, GameSI, true},
		{Meta, false, true, false, false, "#", false, true, "", true},
		{Heading, false, true, false, false, "##", true, true, "", true},
		{Other, false, true, false, false, "", false, false, "", true},
		{Normal, false, true, false, false, "", false, false, "", false},
		{Test, false, true, false, false, "", false, false, "", false},
		{HandoutsPreamble, false, false, false, false, "", false, false, "", false},
		{Appeal, false, false, false, false, "", false, false, "", false},
		{"no-such-kind", false, false, false, false, "", false, false, "", false},
	}
	for _, w := range want {
		k := Of(w.kind)
		got := row{w.kind, Numbered(w.kind), Exported(w.kind), k.Handouts, k.Versioned, k.Marker, k.Section, k.SetsBase, k.Game, k.Pickable}
		if got != w {
			t.Errorf("%s:\n got %+v\nwant %+v", w.kind, got, w)
		}
	}
	if len(want)-1 != len(All) {
		t.Errorf("the table has %d kinds and this test %d: add the new one here", len(All), len(want)-1)
	}
}

func TestCounters(t *testing.T) {
	for kind, want := range map[string]string{Question: CounterQuestion, Theme: CounterTheme, Meta: "", HandoutsPreamble: ""} {
		if got := Of(kind).Counter; got != want {
			t.Errorf("Of(%q).Counter = %q, want %q", kind, got, want)
		}
	}
}

func TestValid(t *testing.T) {
	for _, k := range Names() {
		if !Valid(k) {
			t.Errorf("Valid(%q) = false", k)
		}
	}
	for _, k := range []string{"", "Question", "tema"} {
		if Valid(k) {
			t.Errorf("Valid(%q) = true", k)
		}
	}
}

func TestGameOf(t *testing.T) {
	cases := []struct {
		kinds []string
		want  string
	}{
		{nil, GameChgk},
		{[]string{Question, Meta}, GameChgk},
		{[]string{Question, Theme}, GameSI},
		{[]string{HandoutsPreamble, Theme}, GameSI},
	}
	for _, c := range cases {
		if got := GameOf(c.kinds); got != c.want {
			t.Errorf("GameOf(%v) = %q, want %q", c.kinds, got, c.want)
		}
	}
}

// ForListType is what the board's add-card button makes (cardkind.ts's
// forListType, held to the same answers in xy/jstest/cardkind.test.js).
func TestForListType(t *testing.T) {
	for listType, want := range map[string]string{"si": Theme, "appeals": Appeal, "normal": Question, "test": Question, "": Question} {
		if got := ForListType(listType); got != want {
			t.Errorf("ForListType(%q) = %q, want %q", listType, got, want)
		}
	}
}
