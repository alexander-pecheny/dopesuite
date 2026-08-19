---
status: accepted
date: 2026-08-19
---

# A Protocol's document is parsed and ranked by a module; the shell mounts its lifecycle

After ADR-0015 every game page mounted one shell, synced through one engine and
edited through one cursor — and still held, page-private, the two things a
Protocol's document needs: a reading of it (`ensureState`/`normalizeState`,
ragged arrays padded with `Array.isArray` ladders against an interface the
server never promised) and the arithmetic over it (ОД's totals, rating, shootout
tiebreak and places; КСИ's sticker rules, theme values, score sheet and ranked
results — ≈330 lines reading module-level state). Nothing could call them, so
no test reached `od.ts`, `ek.ts`, `si.ts` or `brain.ts`: 9 067 lines, 60% of the
TS, with the pixel matrix as the only gate. And the document's lifecycle — load
the snapshot, connect the events and the writer on the game-state scope, adopt a
remote state overlaid with the page's un-acked edits, revalidate — was written
out in ОД and КСИ byte for byte (`sync()` was 38 identical lines).

## Decision

- **`od-protocol.ts`, `ksi-protocol.ts`, `brain-protocol.ts`** hold a
  Protocol's document as the page reads it: the state shape, `parseState(raw, …)`
  — the adapter from whatever the server stored to what the renderers may trust
  — and the arithmetic, every function taking the state (and for КСИ the
  `rulesOf(scheme)`) it reads. The page keeps one-line wrappers over its module
  state and its caches; the renderers are untouched. `jstest/*-protocol.test.js`
  drive them with fixture documents.
- **`mountGameDocument(spec)`** in `game-shell.ts` is the lifecycle for a page
  whose whole document is one state blob: it composes the loader, the live
  events and the scoped writer over two page callbacks — `adopt` a fresh
  document, `apply` a remote state — and answers `scope`, `load`, `save`,
  `overlay`, `isPending`. ОД and КСИ call it; the stream is injectable, and
  `jstest/game-document.test.js` runs the load → delta → save path on fakes.
- Брейн keeps its own бой cache. The report proposed it adopt `stage-cache.ts`;
  that module caches *panes* (a container, pane building, prefetch per stage),
  while брейн caches *views* keyed by бой and draws them into one sheet. What
  the two share — the seq-monotonic adopt with the writer's overlay — is five
  lines, and is not worth panes in брейн.

## Consequences

- The first unit tests on the four pages' logic: twelve over the documents,
  one over the lifecycle. A Protocol's invariants are stated in its module,
  not implied by its renderers.
- The places ОД and КСИ show are still computed in the client. ADR-0011 says
  the server ranks; these modules are now the one place a pin to the server's
  ranking would go, and the place to compare the two.
- `od.ts` 3474 → ≈3050 lines, `si.ts` 1468 → ≈1150; the pages are page
  scripts plus wrappers, and a page-private function that reads state is now
  the smell to look for.
