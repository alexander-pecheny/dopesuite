package ui

import (
	"strings"
	"testing"
)

// A two-primitive vocabulary with no design system behind it: the engine's
// contract (vocab merge, validation, expansion, env, mounts) on its own.
const tinyVocab = `{
  "root": "page",
  "enums": {"tone": {"prefix": "Tone", "values": ["calm", "loud"]}},
  "universal": [{"name": "id"}],
  "primitives": [
    {"name": "page", "props": [{"name": "title", "required": true}], "children": "any"},
    {"name": "note", "props": [{"name": "tone", "enum": "tone"}], "children": "text"},
    {"name": "slot", "props": [{"name": "kind", "required": true}], "children": "none"}
  ]
}`

func tinyApp(t *testing.T, overlay string, extra map[string][]PropSpec) *App {
	t.Helper()
	base, err := LoadVocab([]byte(tinyVocab))
	if err != nil {
		t.Fatal(err)
	}
	app, err := NewApp(Options{
		Base:         base,
		VocabOverlay: []byte(overlay),
		ExtendProps:  extra,
		Env:          "the-env",
		Mounts:       map[string]MountSpec{"board": {Tag: "section", Classes: []string{"board-mount"}}},
		Expand: map[string]ExpandFunc{
			"page": func(c *ExpandCtx, p *Element) []Node {
				title, _ := Get(p, "title")
				return []Node{El("html", []Attr{At("data-env", c.Env().(string)), At("title", title)}, c.Nodes(p.Block)...)}
			},
			"note": func(c *ExpandCtx, p *Element) []Node {
				tone, _ := Get(p, "tone")
				return []Node{Inl("p", []Attr{At("class", "note-"+tone)}, c.Items(p.Inline)...)}
			},
			"slot": func(c *ExpandCtx, p *Element) []Node {
				kind, _ := Get(p, "kind")
				m, ok := c.Mount(kind)
				if !ok {
					return []Node{Text("?")}
				}
				return []Node{El(m.Tag, []Attr{ClassAttr(m.Classes...)})}
			},
			"badge": func(c *ExpandCtx, p *Element) []Node { return []Node{Inl("b", nil, Text("badge"))} },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func TestEngine_CompileExpandsThroughEnvAndMounts(t *testing.T) {
	app := tinyApp(t, "", nil)
	out, err := app.Compile("p.dopeui", []byte("page title=\"Hi\"\n  note tone=\"loud\" \"boo\"\n  slot kind=\"board\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<html data-env="the-env" title="Hi">`, `<p class="note-loud">boo</p>`, `<section class="board-mount">`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestEngine_ValidationErrors(t *testing.T) {
	app := tinyApp(t, "", nil)
	cases := map[string]string{
		"unknown tag":    "page title=\"x\"\n  widget\n",
		"bad enum value": "page title=\"x\"\n  note tone=\"shrill\" \"y\"\n",
		"missing prop":   "page\n",
		"unknown prop":   "page title=\"x\"\n  note size=\"3\" \"y\"\n",
		"wrong root":     "note \"x\"\n",
		"unknown mount":  "page title=\"x\"\n  slot\n",
	}
	for name, src := range cases {
		if _, err := app.Compile(name, []byte(src)); err == nil {
			t.Errorf("%s: compiled without error", name)
		}
	}
}

func TestEngine_OverlayAddsButNeverRedeclares(t *testing.T) {
	app := tinyApp(t, `{"primitives": [{"name": "badge", "children": "none"}]}`, map[string][]PropSpec{"note": {{Name: "size"}}})
	out, err := app.Compile("p.dopeui", []byte("page title=\"x\"\n  badge\n  note size=\"3\" \"y\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "badge") {
		t.Errorf("overlay primitive not expanded:\n%s", out)
	}
	base, _ := LoadVocab([]byte(tinyVocab))
	if _, err := NewApp(Options{Base: base, VocabOverlay: []byte(`{"primitives": [{"name": "note", "children": "none"}]}`)}); err == nil {
		t.Error("re-declaring a base primitive was accepted")
	}
	if _, err := NewApp(Options{}); err == nil {
		t.Error("a nil Base was accepted")
	}
}
