package kit

import (
	"strings"
	"testing"
)

func TestBuilder_PageRoundTrip(t *testing.T) {
	doc := &Doc{Nodes: []Node{
		Page(Title("Каталог"), PageFull, Scripts("admin.js"),
			Topbar(Title("Каталог"),
				Iconlink(Href("/x"), Label("Назад"), Text("↩")),
			),
			Section(
				Hint(Text("Привет")),
				Row(SpaceSM, JustifyBetween,
					Button(Ghost, Small(), Text("Копировать")),
					Spacer(),
				),
			),
		),
	}}
	got, err := Render(doc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		`<body class="host">`,
		`<script type="module" src="/static/admin.js"></script>`,
		`<a class="action-icon" href="/x" aria-label="Назад" title="Назад">↩</a>`,
		`<div class="u-row u-gap-sm u-justify-between">`,
		`<button class="btn btn-ghost btn-small" type="button">Копировать</button>`,
		`<div class="u-spacer"></div>`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestBuilder_EnumConstsCarryPropAndValue(t *testing.T) {
	cases := []struct {
		got       Attr
		name, val string
	}{
		{SpaceSM, "gap", "sm"},
		{JustifyBetween, "justify", "between"},
		{AlignCenter, "align", "center"},
		{Ghost, "kind", "ghost"},
		{Danger, "kind", "danger"},
		{PageFull, "kind", "full"},
		{DirCol, "dir", "col"},
	}
	for _, c := range cases {
		if c.got.Name != c.name || c.got.Value != c.val || c.got.Bare {
			t.Errorf("enum const = %+v, want {Name:%q Value:%q}", c.got, c.name, c.val)
		}
	}
}

func TestBuilder_RenderValidates(t *testing.T) {
	doc := &Doc{Nodes: []Node{Col(Row(ID("dup")), Row(ID("dup")))}}
	if _, err := Render(doc); err == nil {
		t.Fatal("expected a validation error for a duplicate id")
	}
}
