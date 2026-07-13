# DopeUIKit — the constrained UI DSL (core spec)

Authoring is typed UI primitives — `page`, `row`, `col`, `button`, `modal` — the
way AppKit/SwiftUI do. HTML tags and CSS classes never appear in pages; they are
render details owned by the expanders. The design system is app-agnostic; each
app adds an overlay (its own primitives, mount kinds, page chrome, CSS layer).

## Two layers (one module)

- **`ui/` — the generic engine.** Grammar/parser, node types, validator,
  vocabulary loader+merger, the expansion framework (`App`/`Options`/`NewApp`/
  `ExpandCtx`/`Chrome`/`ExtendProps`), the deterministic printer, the builder
  machinery, the exported class-free helper surface, and the `uigen` codegen
  library. **The engine holds no vocabulary, no expanders, and no CSS class
  names** — a grep for design-system class literals (`btn`, `host-top`, `u-col`,
  …) in `ui/` is empty. The engine keys the page-root requirement off the
  vocabulary's declared `root` primitive (it doesn't hardcode `page`).
- **`kit/` — the shared design system.** The core `vocab.json`, every core
  expander (page/topbar/iconbtn/col/row/…/modal/dialog), the shared Chrome
  defaults, the generated core builder (`tags_gen.go`, package `kit`), the class-
  vocabulary helpers (`RootAttrs`/`GrowClasses`/`FlexClasses`/`Leaf`/`Input`),
  and the `core.css` + fonts API (`kit.CoreCSS`/`kit.Fonts`). **`kit` registers
  the core through the same public `ui.Options` API the apps use — it is the
  first overlay.** `kit.NewApp` pre-registers the core and layers the app's
  overlay on top; a core-primitive re-declaration by the app overlay still errors,
  and an app `Expand` override of a core primitive still wins.

Apps import `kit` (which re-exports the engine types/helpers they need), never
`ui` directly. The app-generated builder uses `-base pecheny.me/dopeuikit/kit`.

## Architecture

```
.dopeui page ──parse──▶ primitive tree ──validate──▶ (merged vocab)
Go builder ───────────▶      │
                        expand (App expanders)
                             ▼
                       HTML node tree ──print──▶ bytes
```

- **Primitive tree** = the shared `Element` node (`Tag` = primitive name, `Attr`
  = a prop). `parse.go` (`.dopeui`) and `builder.go` (the Go builder) both
  produce it.
- **vocab.json** declares `root` + primitives + typed props + enum tokens + a
  per-primitive children policy (+ `inline`, `placement`). The validator enforces it.
- **Expanders** own every HTML/CSS decision. They live in `kit`
  (`expand_core.go`, `chrome_expand.go`); app `ExpandFunc`s share the same helper
  surface (engine helpers re-exported by `kit` + the kit's class helpers).
- **tags_gen.go** (cmd/uigen) is the generated typed builder (`kit` for the core,
  the app package for an overlay).

## Extension API

```go
app, err := kit.NewApp(kit.Options{         // the core is pre-registered
    VocabOverlay: overlayJSON,              // add primitives / enums / enum values
    ExtendProps:  map[string][]kit.PropSpec{…}, // extra props on existing primitives
    Expand:  map[string]kit.ExpandFunc{…},  // app primitive → HTML nodes (overrides core)
    Inline:  map[string]kit.InlineFunc{…},  // app inline primitives
    Mounts:  map[string]kit.MountSpec{…},   // mount kind → {Tag, Classes}
    Chrome:  kit.Chrome{…},                 // page/topbar knobs (below)
})
app.Compile(name, src)   // .dopeui → HTML bytes
app.Render(doc)          // builder tree → HTML bytes
```

The engine's own constructor, `ui.NewApp(ui.Options{Base: vocab, …})`, takes the
base vocabulary and the full expander tables explicitly; `kit.NewApp` is the
thin wrapper that supplies the core as the base and layers `opts.Expand` over the
core expanders.

- **Vocab merge**: an overlay may add primitives, add enums, and add values to
  existing enums (e.g. app page kinds). Re-declaring a core primitive is an
  error — extension only. `ExtendProps` adds props to an existing primitive
  (the seam for chrome props like dope's page `init`) without re-declaring it.
- **Expander precedence**: an `Options.Expand` entry overrides the core expander
  for the same primitive name (xy overrides `checkbox`/`editor`); other names
  are new primitives. Extensions and core share the helpers, all reached through
  `kit`: `El/Inl/ClassAttr/At/BareAt`, prop getters
  `Get/Flag/IDAttr/Passthrough/CopyProps/CopyFlags/MetaAttrs`, the kit's class
  helpers `RootAttrs/GrowClasses/FlexClasses/Leaf(ctx,…)/Input(ctx,…)`, and the
  recursion on `*ExpandCtx` (`Nodes/Items/Expand/Mount/Chrome/Placement`).
- **ExpandFunc** = `func(ctx *kit.ExpandCtx, p *kit.Element) []kit.Node`.

### Chrome

```go
type Chrome struct {
    Lang, Viewport string
    Stylesheets, FontPreloads, BootScripts []string
    PageKinds   map[string]ui.PageKind // kind → {Body, Main, Frame classes}
    DefaultKind string
    TopbarSync  ui.SyncSpec            // default sync dot; per-page overridable
    HeadHook    func(ctx *ExpandCtx, p *Element) []Node // head nodes after boot
}
```

The core `page` expander emits the doctype, `<html lang>`, head (charset,
viewport, `<title>`, font preloads, stylesheets, boot scripts, **HeadHook**,
then `classicscripts` as `<script defer>` and `scripts` as `<script
type=module>`), and body. `PageKinds[kind]` sets body/main classes and an
optional `Frame` wrapper around main. A `placement:"header"` child (topbar)
becomes `<header>`; `placement:"overlay"` children render after `</main>`.
`topbar` auto-emits the `TopbarSync` dot unless `nosync`; `syncstate` overrides
its `data-state`. `HeadHook` is the seam for an extension to splice head nodes
positionally (dope's `init` marker: `<script>window.__HOST_INIT__=null;</script>`).

**Scripts convention**: page JS that talks over `window` globals with no imports
(dope's whole frontend, xy's `menu.js`) is listed in `classicscripts` (emitted as
`<script defer>`, boot order preserved); genuine ES modules go in `scripts`
(emitted `type=module`). A page mixes both freely.

## .dopeui grammar

Line-oriented, indentation = tree depth (2 spaces/level, tabs forbidden). Per
line: `# …` source comment; `-- …` HTML comment (consecutive lines merge); blank
line preserved; `name prop* inline-item*` a primitive (props first, inline text
last); a line starting with `"`/`(` is an inline-run child. Props: `name="value"`
or bare `name` (flag). Inline items: `"string"` (escapes `\" \\ \n`) or
`(name prop* item*)` nested inline. `page` is the root.

## Tokens (enums)

Each enum in `vocab.json` names a Go const `prefix` used by cmd/uigen. Core
enums: `space align justify button-kind`(primary/ghost/danger/secondary)
`page-kind`(sheet/full — apps add more) `form-dir editor-kind checkbox-kind
modal-variant mount-kind`(empty — apps fill). Values become `Attr` constants
carrying their prop name (`ui.SpaceSM`, `ui.Ghost`, `ui.PageFull`).

These are DSL enums, distinct from the **CSS design tokens** (custom properties)
in `assets/core.css` — colors, spacing, type scale, shadows, and semantic aliases
(`--accent`, `--text-muted`, `--orange-dark` map onto existing theme-aware tokens;
`--shadow-sm`) that the app CSS layers build on. Both apps serve core.css first,
then their own layer.

## Core primitive catalog (summary)

Chrome: `page topbar iconbtn iconlink`. Layout: `col row spacer section`. Text:
`text hint subhead label bigcode message empty muted strong code`. Forms:
`form textfield password filefield hiddenfield numfield colorfield sliderrow
checkbox radio selectfield/option editor button field`. Notable form props:
`textfield`/`password` carry `minlength`/`maxlength`/`pattern`; `selectfield`
takes `name`; `button` supports multi-target form submission via
`formaction`/`formnovalidate` plus `name`/`value` (a `submit` button that posts to
a different action or carries a named value — the numbers page's per-action
buttons). Overlays/compound:
`modal dialog details/summary fieldset datalist tabs/tab/tabpanel list/listrow/
listtitle table/trow/hcell/cell unreaddot mount`. Children policies: `any`,
`text` (inline text/inline-primitives), `content` (text or block — table cells),
`none`, or a named child set.

## Validation (compile errors, file:line)

Unknown primitive; prop not allowed; invalid enum value; flag-vs-value mismatch;
missing required prop; duplicate `id`; children-policy violation.

## Go builder & codegen

`kit.Row(…)`, `kit.Button(kit.Ghost, kit.Small(), kit.Text("…"))`, `kit.Page(…)`.
Enum values are `Attr` constants; string/flag props are ctors. Two primitive
ctors are renamed to avoid clashes (`text`→`Paragraph`, `label`→`SectionLabel`),
the sliderrow `hint` prop is `SliderHint`, the textfield `list` prop is
`InputList`. `cmd/uigen -core kit/vocab.json -pkg kit -base pecheny.me/dopeuikit/
ui` generates the kit's builder over the engine; `cmd/uigen -core <kit vocab>
-overlay app.json -pkg X -base pecheny.me/dopeuikit/kit` generates an app builder:
overlay ctors/consts plus re-export shims for every kit ctor/const, so app code
imports one package. The tool reads both vocabularies from disk (it imports no
design-system package, so it can regenerate the kit's own `tags_gen.go`).

## Testing

`kit/testdata/*.dopeui` are catalog fixtures diffed against golden `.html`
(`go test ./kit -run TestFixtures -update`), plus the kit's compile/overlay/
validate/builder tests; `ui/` keeps the parser tests. Apps exercise the
integration in their own suites.
