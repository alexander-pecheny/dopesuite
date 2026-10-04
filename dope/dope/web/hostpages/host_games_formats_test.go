package hostpages

import (
	"strings"
	"testing"

	"dope/dope/domain/games"
)

// Every registered format has its part of the creation form: a scheme format
// a DSL editor of some height and a hint, a flat format its knobs and how
// they are read. A part for a format nobody registered is an error too.
func TestEveryFormatHasACreationForm(t *testing.T) {
	for _, d := range games.All() {
		f, ok := formatForms[d.Code]
		if !ok {
			t.Errorf("%s: no creation form", d.Code)
			continue
		}
		if d.Flat && (f.knobs == nil || f.read == nil) {
			t.Errorf("%s: a flat format draws and reads its own knobs", d.Code)
		}
		if !d.Flat && (f.rows == "" || f.hint == nil) {
			t.Errorf("%s: a scheme format's DSL editor needs a height and a hint", d.Code)
		}
	}
	for code := range formatForms {
		if !games.Known(code) {
			t.Errorf("%s: a creation form for an unregistered format", code)
		}
	}
}

// The creation form offers every format, the picker to every format that
// keeps an entrant list (Erudit-Sextet included), and a DSL editor exactly to
// those.
func TestCreationFormOffersEveryFormat(t *testing.T) {
	data := hostGameCreateData{SelectedType: games.ES, DSL: map[string]string{}, Entrants: []gameEntrantOption{{ID: 1, Label: "A"}}}
	html := renderPublic(t, hostGameCreateDoc(data))
	for _, d := range games.All() {
		if !strings.Contains(html, `value="`+d.Code+`"`) {
			t.Errorf("no radio for %s", d.Code)
		}
		if strings.Contains(html, `name="`+d.Code+`_dsl"`) == d.Flat {
			t.Errorf("%s: DSL editor present %v, flat %v", d.Code, !d.Flat, d.Flat)
		}
	}
	if !strings.Contains(html, `value="ksi_stickers"`) {
		t.Error("no radio for KSI with stickers")
	}
	if !strings.Contains(html, `data-game-entrants="brain ek es si troika hamsa"`) {
		t.Errorf("entrant picker formats: %s", html[strings.Index(html, "data-game-entrants"):][:80])
	}
}

// The JSON twin of the form posts each format's scheme under that format's
// own field, and the stickers variant reads into an ordinary KSI.
func TestCreateRequestFields(t *testing.T) {
	form := GameCreateRequest{GameType: games.ES, DSL: "[scheme]", Scheme: []byte(`{}`)}.form()
	if form.Get("es_dsl") != "[scheme]" || form.Get("es_scheme") != "{}" {
		t.Errorf("es form: %v", form)
	}
	form = GameCreateRequest{GameType: games.OD, DSL: "[scheme]"}.form()
	if form.Get("od_dsl") != "" {
		t.Error("a flat format takes no DSL on the form")
	}
	f := formatForms[games.KSI].variant
	if f == nil || f.value != ksiStickersGameType {
		t.Fatal("KSI has no stickers variant")
	}
}
