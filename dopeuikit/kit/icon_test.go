package kit

import (
	"strings"
	"testing"

	"pecheny.me/dopeuikit/icons"
)

// A control's icon is compiled into the page, not swapped in by a boot script:
// the whole point of the prop is that the glyph is there in the first byte the
// browser sees.
func TestIconCompilesIntoTheControl(t *testing.T) {
	html := compileIcons(t, `page title="T"
  button icon="trash-2" kind="danger" "Удалить"
  iconbtn id="b" icon="bell" label="События"
`)
	if !strings.Contains(html, `class="ico"`) {
		t.Fatalf("no icon in the output:\n%s", html)
	}
	body, _ := icons.Body("trash-2")
	if !strings.Contains(html, body) {
		t.Errorf("trash-2 shape missing:\n%s", html)
	}
	if !strings.Contains(html, "Удалить") {
		t.Error("a labelled button must keep its words")
	}
	// currentColor is what makes the glyph follow the theme; a hard-coded stroke
	// would be the emoji problem again in another spelling.
	if !strings.Contains(html, `stroke="currentColor"`) {
		t.Error("icon is not stroked in currentColor")
	}
}

// The vocabulary is closed, so a typo is a compile error rather than a blank
// square nobody notices until a user reports it.
func TestUnknownIconNameIsACompileError(t *testing.T) {
	if _, err := iconApp(t).Compile("t.dopeui", []byte(`page title="T"
  button icon="trahs-2" "Удалить"
`)); err == nil {
		t.Fatal("an unknown icon name compiled")
	}
}

// Every name the vocabulary offers must have a shape behind it: icongen writes
// both lists, and this is what catches them drifting apart.
func TestEveryVocabularyIconHasAShape(t *testing.T) {
	for _, n := range icons.Names() {
		if body, ok := icons.Body(n); !ok || strings.TrimSpace(body) == "" {
			t.Errorf("icon %q has no shape", n)
		}
	}
	if len(icons.Names()) == 0 {
		t.Fatal("no icons vendored at all")
	}
}

func iconApp(t *testing.T) *App {
	t.Helper()
	app, err := NewApp(Options{})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	return app
}

func compileIcons(t *testing.T, src string) string {
	t.Helper()
	out, err := iconApp(t).Compile("t.dopeui", []byte(src))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return string(out)
}
