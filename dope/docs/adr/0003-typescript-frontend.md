---
status: accepted
date: 2026-07-23
---

# Game frontend is TypeScript: shared shell + per-protocol renderers

The game pages are rebuilt as one shared **shell** that owns everything format-independent (tabbed game topbar, breadcrumbs, SSE sync/epoch handling, stage navigation, cross-table/bracket/standings views, DopeUIKit styling) plus small per-Protocol **renderer** modules that only build the match grid and handle its edits. The rewrite is in full TypeScript with an esbuild step — ending the no-build, ordered-`<script>`, window-globals convention for game pages. We chose full TS over ES modules + JSDoc because the shell/renderer/scorer contracts are the load-bearing interfaces of the whole design and deserve real checked types; the first brain attempt failed precisely by drifting from unchecked conventions.

## Amendment (2026-07-23, root ADR-0001)

The toolchain moved to the repo root (shared package.json/tsconfig/webbuild),
and the migration pacing changed from renderer-by-renderer to a big-bang
conversion of all legacy scripts to strict-TS ES modules — including the chrome
scripts the scope note below exempted. See `../../docs/adr/0001`.

## Amendment (2026-07-24): bundle emission flips to ESM per ported page

Dope's page bundles stay IIFE + `classicscripts` until a page boots via
`registry.register()` instead of side-effect imports; that porting moment is
when the page's bundle flips to `type=module` ESM (and eventually unlocks
esbuild code-splitting for `match-table.ts`). No standalone emission-conversion
pass — decided 2026-07-24.

## Consequences

- `justfile` dev/test/embed gain an esbuild step; dev hot-reload becomes esbuild watch; embedded assets are built output.
- A protocol renderer cannot ship a page without the standard chrome — conventions become structural, not aspirational.
- One shell serves host and viewer, differing by `CanEdit`.
- Non-game chrome (`menu.js`, `pageforms.js`) may stay classic scripts; only the game-page stack is in scope.

## Amendment (2026-07-24): the DopeShell shim is gone; the real seam is state-sync

`web/ts/shell/` (ProtocolRenderer registry, `window.DopeShell`, its own
StateSync) never gained a call site — every page imported it for side effect
only while re-implementing SSE/epoch/reconnect by hand, three times. The shim
is deleted. The load-bearing seam this ADR wanted now exists as `state-sync.ts`:
one sync engine (`createStateSync` for flat-game state, `createLiveEvents` for
host/viewer scoped dispatch) with an injectable stream adapter, tested through
that interface. A future protocol page builds on state-sync + the shared page
modules rather than a renderer registry.
