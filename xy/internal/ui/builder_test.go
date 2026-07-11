package ui

import "testing"

func TestBuilder_InlineTextAndEmptyElement(t *testing.T) {
	doc := &Doc{Nodes: []Node{
		DoctypeNode(),
		Html(
			ID("root"),
			Head(),
			Body(
				Class(Host),
				Header(Class(HostTop), H1(Text("Привет"))),
				Div(ID("kanban"), Class(Kanban), Hidden()),
			),
		),
	}}
	got, err := Render(doc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := `<!doctype html>
<html id="root">
<head></head>
<body class="host">
  <header class="host-top">
    <h1>Привет</h1>
  </header>
  <div id="kanban" class="kanban" hidden></div>
</body>
</html>
`
	if string(got) != want {
		t.Fatalf("output mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestBuilder_LineForcesInlineRunChild(t *testing.T) {
	doc := &Doc{Nodes: []Node{
		Button(
			ID("notifToggle"), Type("button"),
			Line(Text("🔔"), Span(Class(NotifBadge), ID("notifBadge"), Hidden())),
		),
	}}
	got, err := Render(doc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "<button id=\"notifToggle\" type=\"button\">\n" +
		"  🔔<span class=\"notif-badge\" id=\"notifBadge\" hidden></span>\n" +
		"</button>\n"
	if string(got) != want {
		t.Fatalf("output mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestBuilder_InlineForcesOneLineMixedContent(t *testing.T) {
	doc := &Doc{Nodes: []Node{
		P(Class(AuthHint), Inline(Text("Вход для "), Strong(ID("who")), Text("."))),
	}}
	got, err := Render(doc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "<p class=\"auth-hint\">Вход для <strong id=\"who\"></strong>.</p>\n"
	if string(got) != want {
		t.Fatalf("output mismatch: got %q, want %q", got, want)
	}
}

func TestBuilder_AriaAndDataHelpers(t *testing.T) {
	e := Div(Aria("modal", "true"), Aria("hidden", ""), Data("state", "saved"), Data("edit-only", ""))
	want := []Attr{
		{Name: "aria-modal", Value: "true"},
		{Name: "aria-hidden", Bare: true},
		{Name: "data-state", Value: "saved"},
		{Name: "data-edit-only", Bare: true},
	}
	if len(e.Attrs) != len(want) {
		t.Fatalf("expected %d attrs, got %d", len(want), len(e.Attrs))
	}
	for i, a := range want {
		if e.Attrs[i] != a {
			t.Fatalf("attr %d: got %+v, want %+v", i, e.Attrs[i], a)
		}
	}
}

func TestBuilder_ValidateCatchesUnknownClassToken(t *testing.T) {
	// Class only accepts generated ClassToken constants, so misuse can only
	// be simulated by hand-building an Attr with an off-vocab class value —
	// exactly what the generated builder can never produce, and what
	// Validate must still catch coming from any other tree source.
	doc := &Doc{Nodes: []Node{
		&Element{Tag: "div", Attrs: []Attr{{Name: "class", Value: "not-a-real-token"}}},
	}}
	if _, err := Render(doc); err == nil {
		t.Fatal("expected a validation error for an unknown class token")
	}
}

func TestBuilder_ValidateCatchesInlineElementWithBlockChildren(t *testing.T) {
	inner := Span(ID("x"))
	inner.Block = []Node{Div()}
	doc := &Doc{Nodes: []Node{
		Div(Inline(inner)),
	}}
	if _, err := Render(doc); err == nil {
		t.Fatal("expected a validation error: inline element with block children")
	}
}
