# DopeUIKit — the UI system

A typed `.dopeui` DSL and the design system it renders into, shared by xy and dope. The most important rule here is the layering one: design opinions belong in the Kit and never in the Engine.

## Language

**Engine**:
The generic DSL machinery, in `ui/`: the parser, the validator, the expansion framework, the printer, the typed-builder machinery and the codegen. It knows no vocabulary and no CSS class names. At some point it will be split out into a module of its own.

**Kit**:
The shared design system, in `kit/`: the core Vocabulary, the expanders, Chrome, `core.css` and the fonts. It is the first Overlay on top of the Engine. Apps import the Kit, and never the Engine directly.

**Overlay**:
A thin extension of the Kit belonging to one app, such as `xy/internal/ui` or `dope/dope/web/ui`. It holds that app's own Primitives, its overrides of existing props, and its Mount kinds.

**Vocabulary**:
The set of Primitives, props and enum values that exist, declared in `vocab.json`. The set is closed, and it is enforced: an unknown primitive or prop, an invalid enum value or a duplicate id is a compile error.

**Primitive**:
One typed element of the Vocabulary, such as `page`, `topbar`, `button`, `modal` or `mount`. An expander turns it into markup.

**Chrome**:
The site-wide shell that the Kit provides: the menu, the theme and contrast toggle, and the account links. It also applies the reader's preferences from inside the head, before the first paint, and preloads the body font those preferences name.

Theme and contrast belong to this browser, and the Chrome stores them. The body font belongs to the account instead: in xy it is `users.ui_font`, reported by `/api/auth/me`, which the Chrome already fetches. What the Chrome keeps locally is a copy of that, reconciled on every page load. Letting the reader choose is an app page's job, and in xy the picker is on the profile page.

**Mount**:
A placeholder Primitive that marks where the app's JavaScript takes over at runtime. Each app registers its own mount kinds.

**Page**:
A `.dopeui` source document. The app's `App` compiles it to HTML when the server starts, with `Compile`. Dynamic pages use the same vocabulary, but through the typed builder (`Render`) instead.

**Typed builder**:
The generated Go API over the Vocabulary, in `tags_gen.go`, produced by `go generate` from `vocab.json`. It is the only way a dynamic page may emit markup.

**Rung**:
One step of a uchu ramp, named as `(variant, hue, 1..9)`, for example `yin-9` or `pastel-red-5`. It is the unit we measure colour in. A role always names a rung rather than a hex value, which is what makes "one step darker" a matter of arithmetic. The ramps are vendored in `palette/uchu.json` (ADR-0001).

**Ladder**:
The ordered set of Rungs that one theme's surfaces sit on, from the paper outwards to the control fills. Every theme is a different window onto the same ramps, and the high-contrast theme uses the same roles with a wider gap between them. That is why there is a single palette rather than four separate ones.
_Avoid_: calling this a palette. A palette is the whole vocabulary of colours; a ladder is one theme's path through it.

**Wash**:
A tinted background that says what has happened to a row, a cell or a field. There are exactly three: positive, negative and emphasis. Each one is its own theme's paper mixed with a Rung, so a wash always sits correctly on whatever surface that theme uses.
