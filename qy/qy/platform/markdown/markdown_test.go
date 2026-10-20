package markdown

import (
	"strings"
	"testing"
)

func TestRenderKeepsRawHTML(t *testing.T) {
	got, err := Render("# Title\n\n<details><summary>s</summary>x</details>\n\n| a | b |\n|---|---|\n| 1 | 2 |\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<h1 id="title">Title</h1>`, "<details><summary>s</summary>x</details>", "<table>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}

func TestExpandOnlyKnownNames(t *testing.T) {
	got := Expand("{{year}} and {{ year }} but {{unknown}}", map[string]string{"year": "2026"})
	if got != "2026 and 2026 but {{unknown}}" {
		t.Errorf("got %q", got)
	}
}
