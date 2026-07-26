package hostpages

import (
	"strings"
	"testing"

	dopeui "dope/dope/web/ui"
)

func renderPublic(t *testing.T, doc *dopeui.Doc) string {
	t.Helper()
	html, err := dopeui.Render(doc)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return string(html)
}

// TestPublicIndexDocTrail: / is home, so its 🏠 is the current page rather than
// a link out — and there is no "←" anywhere.
func TestPublicIndexDocTrail(t *testing.T) {
	body := renderPublic(t, PublicIndexDoc([]PublicFestGroup{
		{Title: "Текущие", Fests: []PublicFest{{Ref: "kubok", Title: "Кубок Города", Dates: "15–17 мая"}}},
	}))
	for _, want := range []string{
		`<nav class="crumbs"`,
		`<span class="crumb crumb-home"`, // home is not a link on the page that IS home
		`<span class="crumb crumb-current" aria-current="page">Фесты</span>`,
		`href="/fest/kubok"`,
		`Кубок Города`,
		`15–17 мая`,
		`class="fest-group"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "public-back") || strings.Contains(body, "←") {
		t.Error("the back link should be gone")
	}
}

func TestPublicIndexDocEmpty(t *testing.T) {
	body := renderPublic(t, PublicIndexDoc(nil))
	if !strings.Contains(body, "Нет публичных фестов.") {
		t.Error("missing the empty note")
	}
}

// TestPublicFestDocTrail: a fest hangs off home, and its markdown description
// reaches the page as markup rather than escaped text.
func TestPublicFestDocTrail(t *testing.T) {
	body := renderPublic(t, PublicFestDetailFixture())
	for _, want := range []string{
		`<a class="crumb crumb-home" href="/"`,
		`<span class="crumb crumb-current" aria-current="page">Кубок Города</span>`,
		`<section class="public-description">`,
		`<p>Привет <b>мир</b></p>`,
		`href="/fest/kubok/game/od"`,
		`data-jump-href="/host/fest/kubok"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "&lt;p&gt;") {
		t.Error("the description was escaped; it is already HTML")
	}
	if strings.Contains(body, "public-back") {
		t.Error("the back link should be gone")
	}
}

func TestPublicFestDocNoGames(t *testing.T) {
	d := PublicFestDetail{Ref: "kubok", Title: "Кубок Города"}
	body := renderPublic(t, PublicFestDoc(d))
	if !strings.Contains(body, "В этом фесте пока нет игр.") {
		t.Error("missing the empty note")
	}
}

// PublicFestDetailFixture is the shared sample the trail test renders.
func PublicFestDetailFixture() *dopeui.Doc {
	return PublicFestDoc(PublicFestDetail{
		Ref:         "kubok",
		Title:       "Кубок Города",
		Dates:       "15–17 мая",
		Description: "<p>Привет <b>мир</b></p>",
		Games:       []PublicFestGame{{ID: 1, Slug: "od", Title: "ОД", URL: "/fest/kubok/game/od"}},
	})
}
