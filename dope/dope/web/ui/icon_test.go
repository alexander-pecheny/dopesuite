package ui

import (
	"strings"
	"testing"
)

// The host pages are built with the typed builder rather than from a .dopeui
// file, so TestRealPagesCompile never sees them. This covers that path: an
// Iconbtn carrying a generated icon constant must render the shape, not a blank
// button — which is what the roster's «Редактировать оверрайд» is.
func TestBuilderIconRendersTheShape(t *testing.T) {
	doc := &Doc{Nodes: []Node{Page(Title("t"),
		Iconbtn(IconPencil, Label("Редактировать оверрайд")))}}
	out, err := Render(doc)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := string(out)
	if !strings.Contains(html, `class="ico"`) {
		t.Fatalf("no icon in the rendered button:\n%s", html)
	}
	if !strings.Contains(html, `stroke="currentColor"`) {
		t.Error("icon is not stroked in currentColor, so it will not follow the theme")
	}
	if strings.Contains(html, "✏️") {
		t.Error("the emoji is still there")
	}
}
