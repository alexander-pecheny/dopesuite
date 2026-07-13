# ui v2 — high-level primitive DSL (AppKit-style)

The authoring surface is typed UI primitives — `page`, `row`, `col`, `button`,
`modal` — the way AppKit/SwiftUI do. HTML tags and CSS classes do not appear in
pages; they are render details owned by one place (`internal/ui/render.go`).

Non-goals: pixel parity with the old hand-written HTML. The bar is: looks the
same or better, all JS keeps working, markup is dramatically more maintainable.

## Architecture

```
.xui page ──parse──▶ primitive tree ──validate──▶ (vocab.json)
Go builder ─────────▶      │
                      expand (render.go)
                           ▼
                     HTML node tree ──print──▶ bytes
```

- **Primitive tree** = the shared `Element` node with `Tag` = a primitive name
  and `Attr` = a prop. `parse.go` (the .xui reader) and `builder.go` (the Go
  builder) both produce it.
- **vocab.json v2** declares primitives + typed props + enum tokens + a
  per-primitive children policy. The validator enforces it. render.go does not
  read it — it owns every expansion in Go.
- **render.go** is the ONLY file that knows HTML tags / CSS classes. It expands
  the primitive tree into an HTML `Element` tree, printed by the deterministic
  printer at the bottom of the same file.
- **tags_gen.go** (cmd/uigen) regenerates the typed Go builder from vocab v2.
- **cmd/uic** compiles a `.xui` file to HTML on stdout.
- Server integration is unchanged: `servePage("ui/x.xui")` calls
  `ui.Compile(name, src)` (embed mode caches once at startup; disk/dev mode
  recompiles per request). `versionAssetRefs`/CSP/gzip operate on the bytes.

## .xui grammar (unchanged from v1)

Line-oriented, indentation = tree depth, exactly 2 spaces per level, tabs
forbidden. Per line (after indent):

- `# …` — source comment, ignored.
- `-- text` — HTML comment. Consecutive `--` lines at one indent merge into one
  multi-line comment.
- blank line — preserved between siblings.
- primitive line: `name prop* inline-item*`
  - `name`, prop names: `[a-z][a-z0-9-]*`.
  - prop: `name="value"` (double-quoted) or bare `name` (a flag). Order is
    preserved.
  - **props come first, inline text after** — `button submit "Открыть"`, not
    `button "Открыть" submit`. (The catalog notation below writes the text first
    for readability; the grammar requires it last.)
  - inline-item: `"string"` (escapes `\"` `\\` `\n`) or `(name prop* item*)` — a
    nested inline primitive; parens nest.
- inline-run line: a child line starting with `"` or `(` — items rendered as one
  output line (used for multi-line `hint`/`text`).

`page` is the root: a page file's single meaningful top-level node must be a
`page`. There is no `doctype` token — render.go emits the doctype.

## Tokens (enums)

`vocab.json`'s `enums`; each has a Go const `prefix` used by cmd/uigen.

- `space` (`Space`): `none xs sm md lg xl` → gap classes on the `--space-*`
  scale (`xs`=`--space-1` … `sm`=2, `md`=3, `lg`=4, `xl`=`--space-6`).
- `align` (`Align`, cross axis): `start center end stretch`(default).
- `justify` (`Justify`, main axis): `start`(default) `center end between`.
- `button-kind`: `primary`(default) `ghost` → `Primary`/`Ghost`.
- `page-kind` (`Page`): `sheet full wide board`.
- plus `form-dir`, `editor-kind`, `checkbox-kind`, `doc-kind`, `doc-variant`,
  `modal-variant`, `pane-kind`, `head-kind`, and the closed `mount-kind`.

## Universal props

Allowed on any primitive: `id`, `hidden` (flag), `title` (tooltip), `grow`
(flag → `u-grow`), and the patterns `data-*` / `aria-*`. Widgets that use one of
these names for content (`page`/`topbar`/`modal` `title`) do not also emit it as
a tooltip.

## Primitive catalog

Notation: `name props…` → HTML expansion. Inline text shown first for reading;
author it last.

### Page chrome

- `page title="…" kind="sheet" scripts="a.js b.js" [classicscripts="c.js"]` —
  the root. Emits doctype, `html lang="ru"`, head (charset, the app viewport,
  `<title>`, font preload, styles.css, menu.js, then `classicscripts` as
  `<script defer>` and `scripts` as `<script type="module">`), body. `kind` sets
  body/main classes: `sheet` = body `host import-page`, main `match-main` +
  `sheet-frame import-frame` wrapper; `full` = body `host`, main `match-main`;
  `wide` = body `host`, main `board-main` + an `import-form` wrapper (a centred,
  capped content column on the full-bleed board-main); `board` = body
  `host board-page`, main `board-main`. A `topbar` child becomes the header; `modal`/`docoverlay`
  children render after `</main>` (a preceding comment/blank travels with them);
  everything else renders inside main, in order.
- `topbar title="…" [titleid=] [home=]` → `header.host-top`. With `home`: a
  brand breadcrumb (`a.host-home` 🏠 + `span.host-sep` "/" + `h1.host-title`);
  without it, a plain `h1`. Always auto-emits the sync dot
  (`span.sync-status#status data-state="saved"` "Готово") first in
  `div.host-actions`; `iconbtn`/`iconlink` children follow.
- `iconbtn label="…" [badgeid=]` → `button.action-icon` (`+notif-toggle` with a
  badge) `type=button aria-label=label title=(title|label)`; `badgeid` appends a
  hidden `span.notif-badge`. `aria-*` props pass through.
- `iconlink href="…" label="…"` → `a.action-icon`.

### Layout

- `col`/`row` `[gap align justify wrap]` → `div.u-col`/`.u-row` + `u-gap-*`
  (omitted for `none`), `u-align-*` (not `stretch`), `u-justify-*` (not
  `start`), `u-wrap`.
- `spacer` → `div.u-spacer`. `section` → `section.auth-step`.

### Text

`text`→`p`, `hint`→`p.auth-hint`, `subhead`→`h2.auth-subhead`,
`label [for=]`→`label.card-section-label`, `bigcode`→`p.register-code`,
`message`→`pre.import-message`, `previewtitle`→`h2.preview-title`,
`listtitle`→`span.list-row-title`, `muted`→`span.muted`. Inline primitives
`strong`→`<strong>`, `code`→`<code>`, `muted`→`span.muted` (usable in `(…)`
runs); all may carry `id`.

### Forms & controls

- `form id [dir gap autocomplete method action]` → `form.u-row`/`.u-col` +
  gap (default `dir=row gap=sm`).
- `textfield`/`password id [name placeholder autocomplete spellcheck
  autocapitalize autocorrect required autofocus value maxlength]` →
  `input.input`. `filefield [accept required]`, `numfield [min max step
  placeholder narrow]` (`narrow`→`.lists-move-pos`), `colorfield [value]` →
  `input.label-color-input`.
- `sliderrow id label valueid [min max step hint]` → `div.sizes-row` (head with
  `label.appearance-row-label` + `span.sizes-value#valueid`, then the range
  `input#id`, then `p.sizes-hint`).
- `checkbox kind [checked]` → `label.attach-lossless` (`preview`→
  `.preview-screen-toggle`, `card-preview`→`.card-preview-screen`) wrapping a
  `input[type=checkbox]#id` + text.
- `selectfield id [compact]` → `select.input` (`+card-kind-select`); children
  `option value="…"` → `<option>`.
- `editor id [kind placeholder spellcheck rows readonly name required]` →
  `textarea`; `kind`: default `card-desc`, `comment`→`+comment-input`,
  `handouts`→`+handouts-textarea`, `importsrc`→`input handouts-textarea
  import-textarea`.
- `button [kind small submit disabled href download] "…"` →
  `button.btn`(`+btn-ghost`/`+btn-small`) `type=submit|button`; with
  `href`/`download` → `a.btn`.
- `field label="…"` + one control child → `label.field > span + control`.
- `unreaddot [title]` → hidden `span.unread-dot`.

### Overlays & compound widgets

Overlays are hidden by default (JS reveals them).

- `modal id label [title done doneid variant]` →
  `div.appearance-modal-overlay#id hidden > div.appearance-modal`(+`lists-manage`
  /`sizes-modal`)`[role=dialog aria-modal aria-label]` > optional
  `h2.appearance-modal-title` + children + optional
  `button.appearance-modal-done`.
- `docoverlay id [label kind variant]` — `kind=doc` (default) →
  `div.card-overlay.preview-overlay > div.preview-doc[role=dialog…]`
  (`variant=handouts`→`+handouts-doc`; `variant=import`→ overlay
  `.import-overlay`, doc `.import-doc`); `kind=detail` →
  `div.card-overlay > div.card-detail`. Sub-primitives: `headrow [kind]` →
  `div.preview-head`/`.card-detail-head`; `headactions [kind]` →
  `div.preview-head-actions`/`.card-head-actions`; `previewtitle`.
- `split` → `div.handouts-body`; `pane kind` → `div.handouts-pane`
  (`.handouts-src`/`.handouts-preview`).
- `tabs id` → `div.card-view-tabs[role=tablist]`; `tab id view` →
  `button.card-view-tab[role=tab data-view]`; `tabpanel id` → hidden
  `div.card-view`.
- `list` → `ul.list`; `listrow [href]` → `li.list-row` (or `li > a.list-row`);
  `listtitle`, `muted`. `table` → `table.data-table` (header rows — all `hcell`
  — fold into `thead`, the rest into `tbody`); `trow` → `tr`; `hcell` → `th`;
  `cell` → `td` (text/inline `code`).
- `mount kind="…"` — a JS-owned container rendered empty (may take children,
  e.g. `label-add-row`). Closed `mount-kind` table maps each kind to an existing
  tag+class (`kanban`→`div.kanban`, `token-list`→`ul.token-list`,
  `token-value`→`code.token-value`, …). Extend the table (and the enum) as
  transcription demands; never open an escape hatch to raw HTML/classes.

## vocab.json v2

```json
{
  "enums":     { "space": {"prefix":"Space","values":["none","xs",…]} },
  "universal": [ {"name":"id"}, {"name":"hidden","kind":"bare"}, … ],
  "propPatterns": ["data-*","aria-*"],
  "primitives": [
    { "name":"row",
      "props":[ {"name":"gap","enum":"space"}, {"name":"wrap","kind":"bare"} ],
      "children":"any" },
    { "name":"mount",
      "props":[ {"name":"kind","enum":"mount-kind","required":true} ],
      "children":"any" }
  ]
}
```

A prop is a string value by default, `"kind":"bare"` for a flag, or `"enum":"…"`
for a token set; `"required":true` makes it mandatory. `children` is `"any"`
(block children), `"text"` (inline text / inline primitives / run lines),
`"none"`, or a name list (a closed set of allowed child primitives).

## Validation (compile errors, with file:line)

Unknown primitive; prop not allowed on the primitive (not universal / listed /
`data-*`/`aria-*`); invalid enum value; flag-vs-value mismatch; missing required
prop; duplicate `id`; children-policy violation.

## Go builder (used by dynamic /admin pages)

Generated per primitive: `ui.Row(…)`, `ui.Button(…)`, `ui.Page(…)`. Enum values
are `Attr` **constants** that each carry their prop name, so callers write them
directly: `ui.Row(ui.SpaceSM, ui.JustifyBetween, …)`,
`ui.Button(ui.Ghost, ui.Small(), ui.Text("Создать"))`, `ui.Page(ui.Title("…"),
ui.PageFull, …)`. String/flag props are ctors (`ui.Href(v)`, `ui.Small()`);
`ui.ID`, `ui.Aria(name,v)`, `ui.Data(name,v)`, and the leaves `ui.Text`,
`ui.Line`, `ui.Inline`, `ui.CommentNode`, `ui.Blank` are hand-written.
`ui.Render(doc)` validates then expands+prints; failures the types can't catch
(duplicate id, wrong enum on a primitive) surface there and in tests.

Two generated primitive ctors are renamed to avoid clashes: the `text` primitive
is `ui.Paragraph` (the `text` leaf is `ui.Text`) and the `label` primitive is
`ui.SectionLabel` (the `label` prop is `ui.Label`). The `hint` prop ctor is
`ui.SliderHint`.

## CSS

render.go references classes that already exist in `styles.css`, plus one
appended utilities block (`/* === ui layout utilities === */`): `.u-col .u-row
.u-wrap .u-grow .u-spacer .u-gap-{xs,sm,md,lg,xl} .u-align-{start,center,end}
.u-justify-{center,end,between}`. Nothing else was restyled.

## Testing

`internal/ui/testdata/*.xui` are fixtures exercising the whole catalog (a
sheet page, a board-like page with modals/overlays/tabs/split/mounts, an
admin-like page); `TestFixtures` diffs each against a golden `.html`, regenerable
with `go test ./internal/ui -run TestFixtures -update`. Plus parser tests,
validator tests (one per error class), a byte-exact printer test, and builder
tests.

## Deviations from the original brief

- Enum tokens are `Attr` constants (`ui.SpaceSM`), not typed setters
  (`ui.Gap(ui.Space…)`): Go can't const a struct-valued typed enum without
  func/type name clashes, and the constant form matches the brief's own standalone
  examples (`ui.SpaceSM`, `ui.JustifyBetween`, `ui.PageFull`, `ui.Ghost`). Type
  safety for enum-on-wrong-primitive is enforced by the validator, not the
  compiler.
- The grammar keeps v1's props-before-text order; the catalog's text-first
  notation is illustrative only.
- `cell` holds text / inline `code` (a `"text"` policy), matching the admin
  table; extend to a container policy if a cell ever needs block content.
