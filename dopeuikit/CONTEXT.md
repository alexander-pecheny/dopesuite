# DopeUIKit — the UI system

A typed `.dopeui` DSL and the design system it renders, shared by xy and dope. The layering rule is the whole point: design opinions live in the Kit, never the Engine.

## Language

**Engine**:
`ui/` — the generic DSL machinery: parser, validator, expansion framework, printer, typed-builder machinery, codegen. Knows no vocabulary and no CSS class names; slated to split into its own module.

**Kit**:
`kit/` — the shared design system: core Vocabulary, expanders, Chrome, `core.css` + fonts. The first Overlay on the Engine. Apps import the Kit, never the Engine directly.

**Overlay**:
An app's thin extension of the Kit (`xy/internal/ui`, `dope/dope/web/ui`): its own Primitives, prop overrides, and Mount kinds.

**Vocabulary**:
The closed set of Primitives, props, and enum values, declared in `vocab.json`. Closed means closed: an unknown primitive/prop, bad enum value, or duplicate id is a compile error.

**Primitive**:
One typed element of the Vocabulary (`page`, `topbar`, `button`, `modal`, `mount`, …), expanded into markup by an expander.

**Chrome**:
The site-wide shell the Kit provides: menu, theme/contrast toggle, account links.

**Mount**:
A placeholder Primitive marking where the app's JS takes over at runtime; each app registers its own mount kinds.

**Page**:
A `.dopeui` source document, compiled to HTML by the app's `App` (`Compile`) at server startup. Dynamic pages use the same vocabulary through the typed builder (`Render`) instead.

**Typed builder**:
The generated Go API over the Vocabulary (`tags_gen.go`, `go generate` from `vocab.json`) — the only way dynamic pages emit markup.
