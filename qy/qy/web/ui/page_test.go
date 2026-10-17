package ui

import (
	"strings"
	"testing"
)

func TestRichtextPassesItsHTMLThrough(t *testing.T) {
	doc := &Doc{Nodes: []Node{Page(Title("t"), PageWide, Section(Richtext(Raw("<p><b>bold</b></p>"))))}}
	out, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `<div class="richtext">`) || !strings.Contains(string(out), "<p><b>bold</b></p>") {
		t.Errorf("got %s", out)
	}
}
