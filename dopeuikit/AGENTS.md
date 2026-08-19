# DopeUIKit — Agent Notes

The shared UI system for xy and dope: a typed `.dopeui` DSL and the design system
it renders. `README.md` is the tour, `DESIGN.md` is the spec — read them first.

## Layers (the one rule that matters)

`ui/` is the **generic engine** (parser, validator, expansion framework, printer,
typed-builder machinery, codegen) and knows no CSS class names or vocabulary.
`kit/` is the **shared design system** (core `vocab.json`, expanders, Chrome,
`assets/core.css` + fonts) and is the first overlay on the engine.

- Apps import `kit`, **never** `ui` directly. Each app adds a thin overlay
  (`xy/internal/ui`, `dope/dope/web/ui`) with its own primitives and mount kinds.
- Design opinions belong in `kit/`, never in `ui/` — the engine is slated to split
  into its own module.
- Both apps consume this module via `replace pecheny.me/dopeuikit => ../dopeuikit`,
  so a change here lands in xy and dope on their next build. Check both.

## Codegen

`kit/tags_gen.go` (the typed builder) is generated from `kit/vocab.json`:

```
go generate ./kit          # from dopeuikit/
just generate-check        # from the repo root: fails if tags_gen.go is stale
```

Edit `vocab.json`, regenerate, commit both. Nothing else regenerates it.

## Build / test

This module has no justfile; the root one owns its recipes.

```
just test-uikit         # go test ./...
just vet-uikit
just fmt-uikit
just pre-commit-uikit   # fmt + vet + tidy + generate-check + test
```

## Colour

Colour is uchu (uchu.style), vendored in `palette/uchu.json`. **A role names a
rung, never a hex** — see `docs/adr/0001-colour-is-a-rung-on-the-uchu-ladder.md`
for the rule and its three exemptions.

```
go generate ./palette      # ramps -> core.css, sets -> Go + TS + dope's layer
go test ./palette          # the ladder's invariants, read back out of core.css
```

The generator writes into both app trees by relative path (the `//go:generate`
line in `palette/palette.go` names `dope/dope/web/assets/static/styles.css`,
`xy/web/assets/static/styles.css` and `xy/web/ts/palette_gen.ts`), so it only
runs inside this monorepo layout. Anything that moves `dopeuikit` out of the
repo has to move those app-side outputs behind per-app `go:generate` lines first.

`palette_test.go` is the design's test surface: monotone ladder per theme, ΔL
0.03 between adjacent surfaces, AA for every ink-on-fill pair, high contrast
never compressing. If a colour change fails it, the change is wrong — the
assertions are cheaper to re-read than the four theme blocks are to re-derive.
