---
status: accepted
date: 2026-07-23
---

# Game frontend is TypeScript: shared shell + per-protocol renderers

The game pages are rebuilt as one shared **shell** that owns everything format-independent (tabbed game topbar, breadcrumbs, SSE sync/epoch handling, stage navigation, cross-table/bracket/standings views, DopeUIKit styling) plus small per-Protocol **renderer** modules that only build the match grid and handle its edits. The rewrite is in full TypeScript with an esbuild step — ending the no-build, ordered-`<script>`, window-globals convention for game pages. We chose full TS over ES modules + JSDoc because the shell/renderer/scorer contracts are the load-bearing interfaces of the whole design and deserve real checked types; the first brain attempt failed precisely by drifting from unchecked conventions.

## Consequences

- `justfile` dev/test/embed gain an esbuild step; dev hot-reload becomes esbuild watch; embedded assets are built output.
- A protocol renderer cannot ship a page without the standard chrome — conventions become structural, not aspirational.
- One shell serves host and viewer, differing by `CanEdit`.
- Non-game chrome (`menu.js`, `pageforms.js`) may stay classic scripts; only the game-page stack is in scope.
