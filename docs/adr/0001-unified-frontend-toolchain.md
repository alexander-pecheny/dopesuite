---
status: accepted
date: 2026-07-23
---

# One frontend toolchain and one paradigm: strict-TS ES modules everywhere

After dope's unified-model redesign gave it a TypeScript stack (dope ADR-0003)
the monorepo held three frontend conventions: dope's classic IIFE scripts wired
through `window.*` globals, xy's untyped no-build ES modules, and dopeuikit's
untyped, untested shared assets (menu.js, login.js). An audit found essentially
no copy-pasted code between the apps — the drift was toolchain and convention,
not duplication. We unify both:

- **Toolchain at the repo root**: one `package.json`, `tsconfig.base.json`, and
  `scripts/webbuild/` with per-module targets; `just build-web [target...]` is
  the single entry. Each module keeps its own emission shape — dope bundles
  per-page IIFE into its `static/dist/`, xy transforms per-file to native ESM in
  its `static/dist/`, dopeuikit emits its shared assets' `dist/` for the kit to
  embed.
- **One paradigm**: every first-party frontend source is a strict-TypeScript ES
  module with explicit exports. dope's legacy IIFEs and xy's `.js` modules both
  convert big-bang (strict from day one, no lenient phase); cross-file `window.*`
  wiring goes away except for deliberately published globals, which are declared
  in a `globals.d.ts` shipped from dopeuikit and included by every module's
  typecheck.
- **Tests run against built output**: the frontend test runner imports the
  emitted ESM (build-before-test, sequenced by the justfiles). dope's eval-based
  `browser-module.js` harness is deleted once its IIFEs are gone; shared test
  fakes live with the root toolchain.
- Conversion order: kit assets → xy (annotation only, already ESM; board.js is
  carved into typed `create(deps)` kernels while converting) → dope (paradigm
  change + annotation).

We rejected per-app toolchains (the drift engine this ADR exists to stop),
lenient-then-ratchet typing (two strictness regimes to police), and unifying the
apps' sync engines or CSS layers (genuinely different problems; the shared
layers already live in dopeuikit/dopecore).

## Amendment (2026-07-23): node removed from the toolchain

The original toolchain ran on node: `webbuild.mjs` drove esbuild's JS API, npm
fetched packages, `npm run typecheck` spawned tsc per project, `node --test` ran
the frontend tests. But esbuild is a Go library and tsc 7 is a native binary
delivered through npm packaging — node was only ever glue, and it put ~1.5 s of
runtime startup into every `just dev-web-only`. Node is now out entirely:

- **Bundling is pure Go**: `scripts/webbuild/` (own module, esbuild pinned in
  `go.mod`) replaces `webbuild.mjs`, byte-identical output. The server dev path
  needs only the Go toolchain.
- **Typecheck is a test gate, not a build step**: esbuild strips types without
  checking, so `just typecheck` (all tsconfig projects in parallel, exec'ing the
  native tsc binary directly) runs from `just test`/`pre-commit`, never from
  `build-web`.
- **deno replaces node for the JS-adjacent rest**: `deno install` fetches the
  tsc binary (the one npm package left), and `deno test --parallel` runs the
  frontend suites — measured fastest (4.0 s vs node 4.3 s vs bun 6.7 s on the
  full 235-test run).
