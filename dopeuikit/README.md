# DopeUIKit

A constrained, typed UI DSL and shared design system for the `xy` and `dope`
apps. Two layers, one module: a generic engine and the shared design system on
top of it. Each app adds a thin overlay (its own primitives, mount kinds, page
chrome, CSS layer).

- `ui/` — the **generic engine**: parser (`.dopeui`), node types, validator,
  vocabulary loader/merger, the `App`/`Options` expansion framework, deterministic
  printer, typed-builder machinery, and the `uigen` codegen library. No design-
  system class names, no vocabulary of its own.
- `kit/` — the **shared design system**: the core `vocab.json`, every core
  expander (page/topbar/col/row/button/modal/…), the shared Chrome defaults, the
  generated core builder (`tags_gen.go`, package `kit`), and the `core.css` +
  fonts API. `kit` registers the core through the same `ui.Options` the apps use
  — it is the first overlay. Apps import `kit`, never `ui` directly.
- `assets/` — `core.css` + `fonts/` (Noto Sans woff2); `kit` is their API
  (`kit.CoreCSS`, `kit.Fonts`).
- `ui/uigen/`, `cmd/uigen/` — codegen for the typed builder (core + overlay modes).
- `cmd/uic/` — compile one `.dopeui` file to HTML on stdout (kit core vocabulary).

`DESIGN.md` is the spec. Apps consume it with a local replace:

```
require pecheny.me/dopeuikit v0.0.0
replace pecheny.me/dopeuikit => ../dopeuikit
```

```
go generate ./kit   # regenerate kit/tags_gen.go from kit/vocab.json
go test ./...
```
