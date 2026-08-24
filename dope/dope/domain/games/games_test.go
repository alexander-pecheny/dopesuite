package games

import "testing"

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
