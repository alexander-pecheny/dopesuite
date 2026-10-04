---
status: accepted
date: 2026-10-04
---

# A format's facts live on its registration

ADR-0001, 0012 and 0014 say that registering a `games.Definition` and a
Protocol is enough to add a format. By October 2026 it was not. About 45 places
outside the two registries branched on the game type: `switch gameType`,
`== games.Troika`, SQL `game_type in ('ksi', 'ek', 'es')`. Several of them were
hand-kept lists of formats, and the lists had drifted apart:

- The creation form's entrant picker listed brain, SI, Troika, Hamsa and EK.
  `entrants.Formats` listed the same plus ES, and nothing called it. ES had
  shipped two days before the picker's list was written. The API seated an ES
  Game's chosen entrants all along, and ADR-0023 names ЭС among the buzzer
  Games that keep an entrant list. So the picker's list was the wrong one.
- `HandRosterFormats` (EK, ES, brain, Hamsa) left out SI and Troika, and
  CONTEXT.md says why: SI seats players and Troika seats troikas. That list was
  right.
- The settings page offered зачёты for OD, KSI and Multi but not the
  friendship cup. A cup's teams are its tables, which carry no Flags, so that
  list was right too.
- The pristine documents of the flat formats were built twice, once in
  `gamebuild/legacy.go` when a Game was created and once more in `clear.go`
  when it was cleared.
- The brain export answered 400, because the xlsx switch had no brain case.

## Decision

Everything that code outside the registries asks about a format is a fact on
that format's registration.

- **The Definition** (`domain/games`) holds the facts about the Game's place in
  the fest: what it seats (`Individual`, `Troikas`, `Flat`, and with `Flat`
  whether it keeps an entrant list), `EKBout`, `HandRoster`, `Divisions`,
  `PlayerOverrides`, how it takes a DSL (`DSL`, `DefaultDSL`, `UpgradeDSL`,
  `PastedScheme`), `ToursSeed`, `Title`, `Page`, `Init`, the `Results` view,
  the xlsx layout (`Sheets`) and how the history page reads its edits
  (`Journal`). A layer that may not hold the implementation still asks the
  format which one to use. `gameexport` keeps one builder per `Sheets` layout,
  `pages` keeps one describer per `Journal` style, and `hostpages` keeps one
  part of the creation form per format.
- **The Protocol** (`domain/protocol`) holds the facts about the document a bout
  holds. They are optional capabilities in ADR-0012's pattern:
  `PristineBuilder` (a flat Game's empty scheme and document, plus `ShapeOf`,
  which reads the shape back from a stored scheme, so that creating a Game and
  clearing it build through the same code), `ClearKeeper` (what a clear
  carries over: a cup's players, a Multi's guests), `PlayersUser` (which
  players a bout names, which is what locks them on a hand roster),
  `ScoreMetricer` (the bout score a sheet prints, which the store learns at
  registration in the same way as `RegisterTeamBlob`) and `GuestHost`.
- **No code outside `domain/games` and `domain/protocol` compares a game type
  with a format code**, whether in a `case`, an `==`/`!=` or an SQL
  `game_type = '…'`. `domain/games/guard_test.go` greps for these and fails.
  The lines it allows are listed with their reasons: the migrations, which
  convert historical rows; the fixture fest; `storeutil.ValidateScheme`,
  because storage may not import `domain/games`; the legacy "first KSI" seed
  source; and the OD/KSI counts in a roster import's answer, which are wire
  and journal field names.

## Consequences

- The ES creation form now offers the entrant picker. This is the one change
  a host can see. Every other format's form, export and history are what they
  were.
- A brain Game exports an xlsx: a sheet per stage and a block per bout, with
  each side's marks, the shootout, Σ taken, the place, and the player who
  buzzed on each question.
- The tests that walk the registry say what a new format has left out: a row
  in `formatFacts`, a Protocol that agrees with the Definition, a creation
  form, an export builder, a pristine builder that survives its own shape.
- Some places are still lists that a format must be added to by hand: the
  page kinds in `web/ui/app.go` and `vocab.json`, the bundles in
  `scripts/webbuild`, and the replay codecs. They are tables keyed by format,
  not branches, and `server/pages.go` now derives the game pages from
  `Definition.Page`.
- Questions this ADR leaves open: only brain's settings page edits its DSL,
  while the other formats described by a scheme are rebuilt by deleting them
  and creating them again. Individual SI exports KSI's document sheets,
  although its Games are brackets on EK's page. ES's history lists only the
  coarse events, because the history page reads EK's rows for EK alone. All
  three were kept as they were.

## Amendment, 4 Oct 2026: one package per registry, one place per format

Each format was still split over two packages: its document and arithmetic in
`domain/games/<format>.go`, its Protocol adapter in
`domain/protocol/<format>.go`, which mostly unmarshalled the config and called
`games.*`. The split guarded nothing. `games` was a leaf only because it did
not import `structure` and `store`, and neither of those, nor anything they
import, imports `games`. So `domain/protocol` was folded into `domain/games`:

- A format is `<format>.go` (document and arithmetic) and
  `<format>_protocol.go` (its Definition and its Protocol). The Definition
  carries the Protocol (`Definition.Protocol`), so one registration names
  both, and the registry in `games.go` is one ordered list.
- The per-capability wrappers (`Seats`, `EnteredSeats`, `CanGrow`,
  `GrowSeats`, `UsedPlayers`, `TakesGuests`, `RatingRosterStateKey`,
  `PristineGame`, `ShapeOf`, `KeepsOnClear`, `KeepOnClear`, `FoldRoster`) are
  one generic lookup, `games.As[C](gameType)`. The four whose absence means
  something other than "no" keep their names: `Started`, `UsesFestNumbers`,
  `ValidateEdit`, `EditableWhenFinished`.
- The friendship cup embeds `odSheet`, the part of OD's Protocol the two
  share, where it used to forward four methods to `od{}` by hand. ES embeds
  `ek` as before.
- The guard allows `domain/games/` alone.

Separate packages per format (`domain/formats/<format>/`) were considered and
not taken. Every caller of the registry would need a package that imports all
the formats, and the code several formats share (the roster fold for OD, KSI
and Multi, KSI's participants in Multi) would have to be exported from a core
package. That is a lot of indirection for a split that buys nothing at ten
formats.
