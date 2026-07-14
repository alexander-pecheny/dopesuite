package kit

import (
	"strings"
	"testing"
)

const fakeOverlay = `{
  "enums": {
    "checkbox-kind": { "prefix": "Check", "values": ["fancy"] },
    "mount-kind":    { "prefix": "Mount", "values": ["widget"] },
    "page-kind":     { "prefix": "Page",  "values": ["board"] }
  },
  "primitives": [
    { "name": "banner", "placement": "overlay", "props": [], "children": "text" }
  ]
}`

func fakeApp(t *testing.T) *App {
	t.Helper()
	app, err := NewApp(Options{
		VocabOverlay: []byte(fakeOverlay),
		ExtendProps:  map[string][]PropSpec{"page": {{Name: "init"}}},
		Expand: map[string]ExpandFunc{
			// Override a core primitive's expander (precedence over core).
			"checkbox": func(c *ExpandCtx, p *Element) []Node {
				return one(El("label", RootAttrs([]string{"fancy-check"}, p), El("input", []Attr{At("type", "checkbox")})))
			},
			"banner": func(c *ExpandCtx, p *Element) []Node {
				return one(Leaf(c, "aside", []string{"banner"}, nil, p))
			},
		},
		Mounts: map[string]MountSpec{"widget": {Tag: "div", Classes: []string{"widget"}}},
		Chrome: Chrome{
			Lang:         "ru",
			Viewport:     "width=device-width, initial-scale=1, viewport-fit=cover",
			Stylesheets:  []string{"/static/styles.css"},
			FontPreloads: []string{"/static/fonts/noto-sans-var.woff2"},
			BootScripts:  []string{"/static/menu.js"},
			DefaultKind:  "board",
			PageKinds: map[string]PageKind{
				"board": {Body: []string{"host board-page"}, Main: []string{"board-main"}},
			},
			TopbarSync: SyncSpec{ID: "status", Class: "sync-status", State: "saved", Label: "OK"},
			HeadHook: func(c *ExpandCtx, p *Element) []Node {
				if v, ok := Get(p, "init"); ok {
					return []Node{Inl("script", nil, Text("window."+v+"=null;"))}
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	return app
}

func TestOverlay_MergeAddsAndOverrides(t *testing.T) {
	app := fakeApp(t)
	src := `page title="B" kind="board" init="__HOST_INIT__"
  topbar title="Доска" nosync
    iconbtn label="Меню" "☰"
  checkbox id="c" " on"
  mount kind="widget"
  banner "hi"
`
	out, err := app.Compile("b.dopeui", []byte(src))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	html := string(out)
	for _, want := range []string{
		`<body class="host board-page">`,
		`<main class="board-main">`,
		`<script>window.__HOST_INIT__=null;</script>`, // HeadHook, after boot
		`<label class="fancy-check"`,                  // override wins over core checkbox
		`<div class="widget"`,                         // app mount
		`<aside class="banner">hi</aside>`,            // overlay primitive, placed as overlay
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in:\n%s", want, html)
		}
	}
	if strings.Contains(html, "sync-status") {
		t.Error("topbar nosync should suppress the sync dot")
	}
	// HeadHook marker must sit between the boot script and page body.
	if bi, si := strings.Index(html, "/static/menu.js"), strings.Index(html, "window.__HOST_INIT__"); si < bi {
		t.Error("HeadHook marker should follow the boot script")
	}
}

func TestOverlay_ReDeclareCorePrimitiveIsError(t *testing.T) {
	_, err := NewApp(Options{VocabOverlay: []byte(`{"primitives":[{"name":"row","props":[],"children":"any"}]}`)})
	if err == nil {
		t.Fatal("expected an error re-declaring core primitive row")
	}
}

func TestOverlay_SyncStateOverride(t *testing.T) {
	app := fakeApp(t)
	out, err := app.Compile("s.dopeui", []byte("page title=\"S\" kind=\"board\"\n  topbar title=\"T\" syncstate=\"syncing\"\n"))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(string(out), `data-state="syncing"`) {
		t.Errorf("syncstate override missing:\n%s", out)
	}
}
