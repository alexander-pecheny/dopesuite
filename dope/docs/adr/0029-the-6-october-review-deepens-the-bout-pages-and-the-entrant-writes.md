---
status: accepted
date: 2026-10-06
---

# The 6 Oct 2026 architecture review

The review of 6 Oct 2026 started where ADR-0028 stopped. The bout pages were
still the most-changed code (Тройка 34 commits since 20 Sep, ЭК 24, Хамса 21),
and four of the eight dope candidates were there. This record lists what was
decided for each, in the order they were done. ADR-0028 stays as it is.

## 1. The bout page owns a bout's address

Each bout page addressed a bout its own way: ЭК with `data-bout-anchor`,
Тройка with `id="bout-<letter>"` and a flash, Хамса and брейн with nothing.
Group anchors had two spellings. Link fixes reached ЭК and Тройка four times
and never reached the other two. Since 5d1e6fcf ЭК's grid column heads
linked to `/stage/<code>` at the site root.

- A bout box's id is `bout-<code>` (`boutAnchorID`), a group table's is
  `group-<code>`. `page.boutHref` is `#<tab>@<letter>`, `page.groupHref` is
  `#<tab>@group-<code>`. Old ЭК and Тройка hashes still land.
- Pages answer nothing about tabs. One rule over the tab list finds them: a
  bout lives on its stage's stage, round or protocol tab, and a group on its
  block or pods tab.
- The bout page owns the grid tab (the draw panel is live for hosts on every
  format, брейн included, and every column head links to its tab) and the
  reseed tab (the server's `reseedReady` only, the waiting bouts by letter,
  a heading per panel, and a refusal shown). `FestGridOptions` lost
  `basePath`, `matchTitleLink`, `stageHeaderLink` and `viewer`.

## 2. A stacked bout sheet has one address type

A cell's address was written five times per page: the `data-*`, the cursor's
decoding, its selector, the document path and the presence keys. On Хамса
the presence keys left out `bet`, so another host on a bet cell was drawn on
theme 1, question 1. брейн's cursor spanned every Block's bouts. ЭК's
shootout add and drop bypassed `page.patch`, so undo missed them.

- `stacked-sheet.ts`: a page declares its fields, rows, columns and the path
  of a mark, and the module derives the `data-*`, the cursor geometry, the
  presence kind and its keys, and an `applyMarks` that writes through
  `page.patch`.
- A cell's address is the bout's code plus the fields its row and column
  hold. The presence keys are the same list as the `data-*`. A field may not
  be called `app`, `kind`, `gameID` or `match`, because presence uses those.
- The sheet spans only the bouts on the tab in front.
- Undo of a shootout theme relies on "set the whole theme, null drops it",
  so `matchops` writes a whole theme's answers and players.

## 3. One repaint contract, keyed on the bout's id

There were four ways to repaint and four spellings of a bout's id. ЭК's box
had no id, so the steady redraw (87b7f5a9) never applied on ЭК.
`patchScoreTable` had had no caller since 5d1e6fcf.

- `page.boutBox(code, className)` is the only way to make a bout box. The
  steady redraw finds boxes by `[data-bout]`.
- A page gives `shape(code)` and `repaintCells(code)`. An own edit, the
  server's answer and a remote delta all go through one choice: the same
  shape repaints in place, a new shape redraws the tab, and a bout that is
  not on the tab in front does nothing. Seating is part of the shape.
  Places, pins, bets and lots are repainted.
- A handler reads the bout's state when it fires, not when it was drawn.
- Unticking "finished" opens the bout at once. A host's first arrow key
  selects the first cell.
- `patchScoreTable` and its tests are deleted. `seatingText` lives in
  `ek-seating.ts`.

## 4. The flat sheets read their team view from one lens

ОД, КСИ and Мультиигры each kept the chosen Division, its URL, the chips, the
badges and the sort by hand. КСИ still sorted names with `Intl.Collator`,
which 77e17769 had removed from Мультиигры as a mid-event regression.

- `team-lens.ts` (`createTeamLens`) holds the Division and `?division=`,
  turns a browser move into one render, and answers members, chips, badges
  and order.
- The browser never re-sorts team names. Name order is the server's order.
  Number order puts guests and teams without a number last, ties in the
  server's order.

## 5. A Game's Entrant source is read once

The seed words `random`, `xlsx`, `players`, `fest`, `troikas` and `ksi` were
switched on in five places that disagreed, and `[init]` was read three ways:
compiled, re-parsed from the DSL, and with `json_extract`.

- `domain/imports/source.go` owns `KindOf`, `Declared` (read from the
  compiled scheme; `schemedsl.ReadInit` is the only parser), `SourceFor`,
  `Source.Seeder` and `DefaultEntrants`. It sits in `imports` because
  `entrants → gamebuild → imports`. `entrants`' source and kind names are
  aliases of these.
- Where the copies disagreed:
  - A Тройка Game with nothing recorded takes its default troikas on a
    rebuild, clear or recompile, never the fest's teams.
  - The `fest` source on a Тройка Game lists troikas.
  - The Тройка roster tab's last fallback follows the Game's Division.
  - `seed: fest`, `troikas` and `ksi` are source words that creation accepts.
- The fixture fest now writes its teams as hand teams. Its old rows had no
  rating id and no hand mark, so every roster write deleted them, as ADR-0024
  intends. Production never had such rows.

## 6. Game settings rules live in `festops`

The rule that only a `DSLEditable` format's scheme may change was checked in
the JSON twin only, so posting the form could recompile a flat Game. The
hidden-Division merge was in the form only. The settings page refilled a
refused DSL from a field renamed in 2ac005e6, so the host's edit was lost.

- `festops.UpdateSettingsTx` enforces the scheme rule for both twins.
  Sending back the stored scheme is not a change.
- `Settings.ShownDivisions` carries the form's intent, and festops works out
  what is hidden: every offered Division not ticked, plus every one hidden
  before that is no longer offered. `HiddenDivisions` stays an explicit full
  list for the twin.
- `domain/roster/divisions.go` is where Divisions live: the fest's list,
  membership (the leading-minus rule) and cleaning. Membership is decided in
  Go, not in SQL.

## 7. A fest-roster write owns its follow

Three host-page transactions ended with the follow and a hand-built
broadcast, and `saveFestTeamFlags` was a whole domain transaction inside
`hostpages`.

- `entrants/festroster.go` holds every fest-roster write that can move a
  troika: add, save, delete, Flags, roster edit, recompile and the rating
  import. Each runs its write and then the follow. The follow is unexported.
- `core.FestWrite.Broadcast` lists the documents, ЭК rosters and Games whose
  view changed, and hostpages' `commit` sends what it is handed. A troika
  write's views are all the fest's Тройка Games.
- Two bugs went with it: a settings save told only the edited Game, not the
  ones the follow re-seated, and deleting a troika ran the follow twice.

## 8. Not done: ЭК's document stays in `store`

The review proposed moving ЭК's document, projection and scorer next to
`ek.go` behind a Protocol capability. Reading the code showed it would make
things worse.

- The scorer produces `store`'s shared view types (`MatchView`,
  `ParticipantView`, `MatchState`), which the legacy engine, the export,
  festview and `places_test` read. `games` imports `store`, so `store`
  cannot forward to it.
- `MatchBlob` is a storage type. `festwrite`, `migrate` and `journal` write
  it, and moving it into `domain/games` would make storage import the
  domain.
- Most of the `TeamBlobShaped` branches are about how a document keyed by
  Participant is written and replayed, not how it is projected. A capability
  would leave them all.
- `EKBout` and `TeamBlob` are different facts: личная СИ has a team blob but
  no ЭК bout. The comment on `EKBout` now says so.

A real move would first split the view types out of `store` and take the
legacy single-match engine off `store.MatchState`. That is a separate piece
of work. ADR-0012's `TeamBlob()` and ADR-0027's file rule both still hold,
with ЭК as the exception ARCHITECTURE.md already describes.

## Follow-ups

The cleanup commits after these name the bouts in a refused reseed by letter
and fix the smaller gaps each step left. They change no decision above.

One of them finishes §5: a recompile of a Game with no recorded Entrant
list seats and records the list a clear would (the troikas its seats hold
first, then its default troikas; the fest's roster for a team Game with no
seats). Before, a Тройка made for an empty зачёт and then given a scheme
without `division:` was compiled for every troika and seated none of them.
A Game without a division still does not follow the fest's troikas: a troika
added later waits for the host on the entrants tab.
