---
status: accepted
date: 2026-08-18
---

# A page mounts one shell, syncs through one engine and edits through one cursor

The 18 Aug 2026 architecture review's page-side items are one decision in
four parts: the four game pages (ЭК, ОД, КСИ, брейн) keep only what is theirs
— their data adopters and renderers — and share everything a game page does
alike, each in one module with one implementation.

- `match-table.ts` and its 76-member `DopeTable` re-export desk are gone;
  `cells.ts`, `score-table.ts`, `standings.ts`, `venue.ts`, `fest-roster.ts`
  and `ek-stats.ts` each own their symbols, and a page imports by name from
  the module that owns one (`50ebdbd`).
- `state-sync.ts` exports two primitives every page composes.
  `createLiveEvents` reads: a scope map says what a delta chains onto and
  who adopts the result; the engine owns seq dedupe, gap reporting, the
  epoch reset, the sibling-game rule and wake recovery. `createScopedWriter`
  writes: cell patches coalesced per scope per window, structural sends
  carrying an intent, both overlaid on every view until acked, retried,
  persisted, flushed on hide. `createStateSync` (one blob per page) and ЭК's
  hand-built pending map are gone; ОД and КСИ register one game-state scope,
  ЭК and брейн one per бой; брейн gained the write discipline it lacked
  (`6668358`).
- `sheet-cursor.ts`'s `createSheetCursor(spec)` is the one active-cell
  selection: a page describes its grid — rows and columns, ragged on either
  axis, a cell's coordinate and back — and applies values; the cursor owns
  ranges, arrows with clamping, Home/End, the mark keys and Delete on the
  selection, copy and paste as a tab grid, the tap-cycle, the active cell and
  its row highlight. ЭК's spill from a бой's last team into the next is the
  stacked stage sheet's ordinary arithmetic; the mark tokens are one table
  (`ee55141`).
- `game-shell.ts`'s `mountGamePage(spec)` mounts what every page mounts:
  the ☰ links, the banner, the status dot, the recorder, the header trail
  and title, and host presence whose cursors are declared element kinds —
  a selector and the data-* keys the sheet cursor addresses cells by. `host`
  is the route's, never passed (`2b88039`).

## Considered options

Keeping `createStateSync` as a thin one-scope wrapper for ОД/КСИ was rejected:
it would have left a third spelling of "how a page syncs" and its tests
testing the wrapper. Letting the writer own only cell patches was rejected:
brain's finish tick and ЭК's structural writes would have kept a second
overlay path and the "operators re-click" failure alive. Converging the
pages' touch input (ОД's keypad, ЭК's nav bar over the place inputs, tap-only
marks elsewhere) was deferred: it is a product call, and the cursor keeps
each page's affordance meanwhile. A shell was deleted once (`9c7f503`, "with
zero call sites") — it was a speculative shim; this one is extracted from
four live copies, which is why the second one lives.

## Consequences

- A page that hand-rolls a keydown for arrows or marks, a pending queue, a
  presence adapter, its own breadcrumb trail, or a status-dot state machine
  is a regression; a fifth page starts by mounting the shell and describing
  its sheet and its scopes.
- The presence cursor's values travel as strings (the data-* attribute
  values); a client of an older build reads them for one deploy.
- The status dot has one precedence for all pages: reconnecting, then a
  failed write nothing superseded, then saving, then saved.
- On a КСИ or ОД page an epoch reset resyncs the game-state scope from its
  state endpoint; on ЭК and брейн it reloads the page, since their per-бой
  caches merge monotonically by seq.
- The pixel matrix shoots viewer pages of the studchr fest, which has no КСИ
  game and no host page; both are checked by hand until the fixture grows
  (a КСИ page threw at load for a day before this series found it).
- Found and fixed on the way: брейн's keyboard marks were dead since the
  tabs moved to `game-tabs` (the handler compared the tab key
  `protocol:<block>` to the word); every КСИ page threw at load since
  `7bf4f85` (`activeTab` read `TABS` before it existed; hotfix `a375e14`).
