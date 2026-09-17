# DopeUIKit — Agent Notes

This is the shared UI system for xy and dope: a typed `.dopeui` DSL and the
design system it renders into. Read `README.md` first for the tour, and
`DESIGN.md` for the specification.

## Layers

This is the rule that matters most. `ui/` is the **generic engine**: the parser,
the validator, the expansion framework, the printer, the typed-builder machinery
and the codegen. It knows no CSS class names and no vocabulary. `kit/` is the
**shared design system**: the core `vocab.json`, the expanders, Chrome, and
`assets/core.css` with the fonts. It is the first overlay on top of the engine.

- Apps import `kit`, **never** `ui` directly. Each app adds a thin overlay
  (`xy/internal/ui`, `dope/dope/web/ui`) with its own primitives and mount kinds.
- Design opinions belong in `kit/` and never in `ui/`, because the engine is
  going to be split out into its own module. The page Chrome (`kit.Chrome`,
  `PageKind`, `SyncSpec`, `HeadLink`) is a kit type, and the engine carries it
  without looking inside, as `Options.Env`. Expanders read it back out with
  `kit.ChromeOf(ctx)`. An app declares its own Chrome by writing
  `kit.CoreChrome().With(delta)`. `ui/engine_test.go` tests the engine on its
  own, against a vocabulary of just two primitives.
- Both apps consume this module via `replace pecheny.me/dopeuikit => ../dopeuikit`,
  so a change here lands in xy and dope on their next build. Check both.
- The kit imports `dopecore`, and dopecore imports neither the kit nor `ui`.
  That is why plumbing which faces the apps but needs to know about the kit lives
  in `kit/` instead of being copied into both apps (root `docs/adr/0004`). It
  consists of: `kit.Assets`, the webassets config with core.css, the fonts,
  login.js and menu.js already wired in; `kit.PageSet`, which compiles `.dopeui`
  pages either once or per request, and whose `Provide` registers a source that
  is not a file on disk; `kit.LoginPage(title, redirect)`, the login page source
  in `assets/ui/login.dopeui`, which sits next to the `login.ts` whose ids it has
  to carry, and which both apps serve at /login; `kit.AdminCreateUsers`,
  `kit.SortHeader` and `kit.AdminTime`, which the /admin pages share; and
  `uitest.PageContract`, the test each app runs over its own real pages plus the
  provided ones, checking that they compile and that the ids and the markup its
  scripts look for are there.

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

The colours come from uchu (uchu.style) and are vendored in
`palette/uchu.json`. **A role always names a rung on that ladder, never a hex
value.** See `docs/adr/0001-colour-is-a-rung-on-the-uchu-ladder.md` for the rule
itself and the three cases exempt from it.

```
go generate ./palette      # ramps -> core.css, sets -> Go + TS + dope's layer
go test ./palette          # the ladder's invariants, read back out of core.css
```

The generator writes into both app trees by relative path (the `//go:generate`
line in `palette/palette.go` names `dope/dope/web/assets/static/styles.css`,
`xy/web/assets/static/styles.css` and `xy/web/ts/palette_gen.ts`), so it only
runs inside this monorepo layout. Anything that moves `dopeuikit` out of the
repo has to move those app-side outputs behind per-app `go:generate` lines first.

`palette_test.go` is where the design itself is tested. It checks that the
ladder is monotone within each theme, that adjacent surfaces differ by ΔL 0.03,
that every pairing of ink on fill passes AA, and that the high-contrast theme
never compresses the range. If a colour change makes that test fail, the change
is wrong. It is much quicker to read the assertions than to work the four theme
blocks out again.
