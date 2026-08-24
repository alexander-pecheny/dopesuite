# Every Transfer goes through a Bundle

## Status

Accepted, 2026-08-24. Extends
[ADR-0013](0013-a-board-bundle-is-a-plaintext-zip.md).

## Context

xy had grown three separate ways to write content onto a board that did not
author it:

- `bundleimport.ts` — an archive from another instance, into a new board.
- `transfer.ts` + `movelist.ts` — a card, a run of cards or a whole list, moved
  or copied onto another board this device holds the key to.
- `runImport` in `import.ts` — a Trello board, into a new board.

All three do the same job: decrypt or read plaintext from somewhere, re-encrypt
every field under the destination key, hand out fresh ids, and reconcile the
two things that can already exist on the destination — a Label and a Test
Session. Each had written that loop itself, and they had already drifted: only
the archive path carried description-edit history, only the live path stamped a
copied Session's `origin`, only the Trello path tolerated an attachment it
could not download, and the Trello path was still sending a label `kind` the
server stopped reading long ago.

Then partial export and partial import (ADR-0013) needed a fourth combination —
a subset of a file, onto a board that already exists — and there was no honest
place to put it.

## Decision

`Bundle` is the one intermediate representation, and `applyBundle` is the one
write path. Producers turn a source into a Bundle; the applier turns a Bundle
into rows on a board. Neither knows about the other.

```
.zip archive  ─┐
live board    ─┼─▶ Bundle ─▶ applyBundle(target, bytesOf) ─┬─▶ a new board
Trello        ─┘                                           └─▶ an existing one
```

- **Producers are pure-ish readers.** `readBundleFile` (zip), `buildBundle`
  (a live board, ticked Lists) and `trelloBundle` (the API or a JSON export)
  each return a `Bundle` and a `bytesOf`. None of them writes anything.
- **Attachment bytes stay off the heap.** They arrive through
  `bytesOf(attachment)` rather than inside the Bundle, so moving a list does
  not materialise a board's worth of раздатки in memory. A producer that
  returns `null` is saying "this one cannot be had" — a Trello download that
  404s — and the apply carries on and names it; a producer that must have the
  file throws instead.
- **The target is data, not a code path.** `append: null` means a board created
  for this Bundle: ranks and rows verbatim, one Label and one Session per row.
  An `AppendState` means an existing board: lists re-ranked into place, Labels
  and Sessions reconciled against what is already there.
- **The live cross-board copy is a Bundle that never becomes a file.**
  `movelist.ts` builds one from the list being moved and applies it to the
  destination. Only the same-board *move* stays outside — it is a re-rank, not
  a Transfer, and it is the one such operation that works offline.

## Consequences

- Moving or copying a list to another board now carries description-edit
  history and the metadata trail, not just comments and attachments. That is
  the archive path's behaviour winning; it is what "the list travels" should
  always have meant.
- Trello import loses the label `kind` classification it was computing —
  `createLabelRequest` has had no such field for a long time, so the green/red
  scan was writing nothing.
- Trello content can now be appended to an existing board with no new code.
  It is deliberately not exposed: /import makes new boards, and that is the
  whole of what its page promises.
- A per-card failure during a Trello import is no longer swallowed. The List is
  the unit now (ADR-0013), so a card that will not create takes its list back
  and reports; only an attachment is allowed to be missing and shrugged at.

## Alternatives considered

**Share the reconcilers only** — lift `reconcileLabels` / `reconcileSession` /
the attachment re-encrypt into a module the three loops import. Much the
smaller diff, and it fixes the reconcile drift. It leaves three loops to drift
in every other way, and gives the new "a subset of a file onto an existing
board" case nowhere to live but a fourth loop.

**Route the per-card paths too** — `carddetail`'s single-card move and
`masspanel`'s bulk move as one-card Bundles. Total uniformity, at the price of
churning the most-used and best-tested write paths in the app for nothing the
reader would notice. They keep `transfer.ts`.
