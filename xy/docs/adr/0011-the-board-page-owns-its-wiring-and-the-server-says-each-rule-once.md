---
status: accepted
date: 2026-08-19
---

# The board page owns its wiring, not its features; the server says each rule once

After ADR-0010 the board page still held three features the panel registry
had not reached: xy's only screen rendering of a question (≈200 lines, no
exports, no test), the open card's «Метки»/«Тесты»/«Видели» surface (≈280
lines that called back into two panels the page owned), and the 🔔 bell with
its own copy of the лента's verbs map. The card editor carried the cross-board
move (≈390 lines) and exported eight methods for it; two panels imported the
editor only to reach them, and the editor's dialog and the list panel repeated
the transfer body with their own ranks. On the server, the unread rule was a
string fragment in four files and had drifted (a deleted comment kept a board
badged), and the Timeline had five inserts with their own column lists and
two scanners over the same twelve columns.

## Decision

- **A feature is a module with a `create…(board, ui, deps)` factory**, taking
  its nodes as a ui record and the board's state, verbs and lookups through
  the `Board` seam, exactly as ADR-0010's panels do: `preview.ts`
  (`renderPreviewCard`, `renderRich` — the ✏️ builder is passed in),
  `cardlabels.ts`, `bell.ts`, `transfer.ts`. `board.ts` keeps the render loop,
  the drag, the boot and the wiring (1787 → 1238 lines). `timeline.eventVerb`
  is the one verbs map.
- **`transfer.ts` is the one transfer path**: `transferCard(card, list, ctx,
  remove, rank?)` returns the new id; the card dialog passes its slot, the
  list panel loops over its cards, the mass panel calls it once per card.
  `CardDetail` is an editor again (seven methods).
- **`unread.go`** names the two buckets, the watermark and the Mention as SQL
  fragments; the board list, the snapshot, the activity feed and «Прочитать
  всё» compose them. **`timeline.go`** has the one writer (`insertEvent`,
  every kind's columns; `appendEvent` stays as the metadata trail's wrapper)
  and the one reader (`readTimeline`).

## Consequences

- The renderer, the labels surface and the transfer have their first tests
  (`preview.test.js`, `cardlabels.test.js`, `transfer.test.js` with real keys),
  and the board-list test now deletes the comment and expects the badge gone.
- `fillPreviewImages` queries `.pv-img-missing` alone (the attribute selector
  the DOM shim cannot match); the DOM shim matches tags with digits (`h2`).
- A page-private function that reads `state` is the smell to look for in
  `board.ts`; the next lump (the list preview overlay itself, the rename and
  delete actions) would take the same shape.
