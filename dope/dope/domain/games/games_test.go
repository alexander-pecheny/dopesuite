package games

import (
	"strings"
	"testing"
)

// A game type's page is one datum: the viewer route, the host route and the
// lockdown snapshot all serve the same HTML and boot the same init payload.
func TestPageOfEveryGameType(t *testing.T) {
	cases := []struct {
		code string
		page string
		init InitKind
	}{
		{EK, "static/ek.html", InitEK},
		{OD, "static/od.html", InitGame},
		{KD, "static/od.html", InitGame},
		{KSI, "static/si.html", InitGame},
		{Brain, "static/brain.html", InitGame},
		// Личная СИ borrows ЭК's page for its bracket, not КСИ's blank.
		{SI, "static/ek.html", InitEK},
		// Мультиигры is a flat game like КСИ; Тройка plays a bracket, so it
		// boots the bracket init on a page of its own.
		{Multi, "static/multi.html", InitGame},
		{Troika, "static/troika.html", InitGame},
		// An unknown or empty type falls back to the default format's page.
		{"", "static/ek.html", InitEK},
		{"kvrm", "static/ek.html", InitEK},
	}
	for _, c := range cases {
		d := Get(c.code)
		if d.Page != c.page || d.Init != c.init {
			t.Errorf("Get(%q) = page %q init %v; want %q %v", c.code, d.Page, d.Init, c.page, c.init)
		}
	}
}

// formatFacts is every registered format's yes/no facts, written out. A new
// format must add its row here, which makes whoever adds it decide each one
// rather than inherit a zero value.
var formatFacts = map[string]struct {
	individual, troikas, ekBout, flat, handRoster, divisions, overrides, pasted, toursSeed bool
}{
	OD:     {flat: true, divisions: true, toursSeed: true},
	KSI:    {flat: true, divisions: true, overrides: true},
	Brain:  {handRoster: true},
	EK:     {ekBout: true, handRoster: true, overrides: true, pasted: true},
	ES:     {ekBout: true, handRoster: true, overrides: true, pasted: true},
	SI:     {individual: true},
	Multi:  {flat: true, divisions: true},
	Troika: {troikas: true},
	Hamsa:  {handRoster: true},
	KD:     {flat: true},
}

// Every registered format declares every fact the code outside the registry
// asks: its names, its page, how it takes a DSL, its export and its history,
// and the yes/no facts in formatFacts.
func TestEveryFormatDeclaresItsFacts(t *testing.T) {
	if len(All()) != len(formatFacts) {
		t.Errorf("the registry has %d formats and formatFacts %d rows", len(All()), len(formatFacts))
	}
	for _, d := range All() {
		want, ok := formatFacts[d.Code]
		if !ok {
			t.Errorf("%s: no row in formatFacts", d.Code)
			continue
		}
		got := struct {
			individual, troikas, ekBout, flat, handRoster, divisions, overrides, pasted, toursSeed bool
		}{d.Individual, d.Troikas, d.EKBout, d.Flat, d.HandRoster, d.Divisions, d.PlayerOverrides, d.PastedScheme, d.ToursSeed}
		if got != want {
			t.Errorf("%s: facts %+v, want %+v", d.Code, got, want)
		}
		if d.Label == "" || d.Title == "" || d.Page == "" {
			t.Errorf("%s: label %q, title %q, page %q: each must be set", d.Code, d.Label, d.Title, d.Page)
		}
		if d.DSL == 0 || d.Sheets == 0 || d.Journal == 0 {
			t.Errorf("%s: DSL %d, Sheets %d, Journal %d: each must be declared", d.Code, d.DSL, d.Sheets, d.Journal)
		}
		// A flat Game is built from its own knobs: it has no scheme to
		// prefill, paste or upgrade, and keeps no entrant list to hand-roster.
		if d.Flat && (d.DefaultDSL != nil || d.UpgradeDSL != nil || d.PastedScheme || d.HandRoster || d.DSL == DSLEditable) {
			t.Errorf("%s: a flat format declares a scheme-format fact", d.Code)
		}
		if d.HandRoster && (d.Individual || d.Troikas) {
			t.Errorf("%s: a hand roster is a team's, but the format seats players or troikas", d.Code)
		}
		if d.DefaultDSL != nil && d.DefaultDSL(8) == "" {
			t.Errorf("%s: DefaultDSL gives an empty scheme", d.Code)
		}
		if KeepsEntrantList(d.Code) == d.Flat {
			t.Errorf("%s: KeepsEntrantList disagrees with Flat", d.Code)
		}
	}
}

// The lists that used to be kept by hand, and disagreed, are each one fact now.
func TestTheFormatListsThatUsedToDisagree(t *testing.T) {
	cases := []struct {
		name string
		pred func(Definition) bool
		want []string
	}{
		// ADR-0023: every buzzer Game keeps an entrant list — ЭК, ЭС, личная СИ,
		// брейн, Тройка, Хамса. The creation form's picker used to leave ЭС out
		// (it was written two days after ЭС shipped), while the API seated an
		// ЭС's chosen entrants all along.
		{"entrant list", func(d Definition) bool { return KeepsEntrantList(d.Code) }, []string{Brain, EK, ES, SI, Troika, Hamsa}},
		// A flat format refuses a chosen entrant list.
		{"flat", func(d Definition) bool { return d.Flat }, []string{OD, KSI, Multi, KD}},
		// CONTEXT.md, Состав в игре: the team buzzer formats. Личная СИ seats
		// players and Тройка troikas, whose people the troikas page edits.
		{"hand roster", func(d Definition) bool { return d.HandRoster }, []string{Brain, EK, ES, Hamsa}},
		// The formats whose results carry зачёт chips. A friendship cup's
		// tables carry no Flags.
		{"divisions", func(d Definition) bool { return d.Divisions }, []string{OD, KSI, Multi}},
		{"individual", func(d Definition) bool { return d.Individual }, []string{SI}},
		{"EK's bout", func(d Definition) bool { return d.EKBout }, []string{EK, ES}},
		{"DSL refused", func(d Definition) bool { return d.DSL == DSLRefused }, []string{Multi}},
		{"DSL edited on the settings page", func(d Definition) bool { return d.DSL == DSLEditable }, []string{Brain, EK, ES, SI, Troika, Hamsa}},
	}
	for _, c := range cases {
		got := Codes(c.pred)
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// The creation form's default schemes are the ones it always offered.
func TestDefaultSchemes(t *testing.T) {
	if got := Get(Brain).DefaultDSL(6); got != "[defaults]\nquestions: 5\n\n[scheme]\nkind: roundrobin\ngroup_size: 6\n" {
		t.Errorf("brain default: %q", got)
	}
	if got := Get(Brain).UpgradeDSL(6, `{"questions":7}`); got != "[defaults]\nquestions: 7\n\n[scheme]\nkind: roundrobin\ngroup_size: 6\n" {
		t.Errorf("brain upgrade: %q", got)
	}
	if Get(EK).DefaultDSL != nil || Get(ES).DefaultDSL != nil {
		t.Error("EK and ES offer an empty editor")
	}
	for _, code := range []string{SI, Troika, Hamsa} {
		if Get(code).DefaultDSL == nil {
			t.Errorf("%s: no default scheme", code)
		}
	}
}
