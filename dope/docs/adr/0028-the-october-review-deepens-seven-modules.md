---
status: accepted
date: 2026-10-05
---

# The 5 Oct 2026 architecture review

The review of 5 Oct 2026 looked at the parts of dope that changed most since
mid-September: Тройка, the Entrant list, the bout pages and `gamebuild`. It
found seven places where a module was shallow or where its rules had leaked
into its callers. This record lists what was decided for each, in the order
they were done.

## 1. Who a Game seats is decided in `entrants`

Seven modules answered the question "after this host action, who sits
where": `entrants`, `imports`, `gamebuild`, `overrides`, `roster`, `games` and
`hostpages`. The Тройка Games that follow a зачёт were re-seated from six
places in `hostpages`, and after a rating import that re-seat ran in a second
transaction, because `imports` cannot call `gamebuild`. Two exported seed
functions, `ImportSeeds` and `SetSeedImportDeclined`, were called only by
tests. They skipped the rebuild and the edit log, so those tests exercised a
path production never takes.

- `domain/entrants` holds every write that changes an Entrant list: the
  entrants tab, `FollowDivisionsTx` (was `gamebuild.SyncDivisionEntrantsTx`),
  `DeleteTroikaTx` (the follow, the drop from the lists and the delete, which
  `hostpages` used to sequence) and `ImportFestRoster`. Each saves the list
  through one `applyListTx`.
- `gamebuild` keeps only the Structure's half of that: `FollowListTx`
  rebuilds a Game sized by its entrants.
- A rating import re-seats the following Games inside its own transaction.
  `imports.RosterChoice.Within` is how it reaches them: `entrants` passes the
  follow in, since `imports` sits below it.
- The two seat tests are named and documented in `imports`.
  `PlayedParticipants` asks whether an entrant has results, and so whether
  the list may still move it. `InBoutTx` asks whether an entrant sits in any
  bout at all, and so whether it can be deleted.
- `ImportSeeds`, `SetSeedImportDeclined`, `entrants.Formats` and
  `gamebuild.EntrantDivision` are gone. The tests import through
  `entrants.ImportLegacy` and decline through `entrants.Decline`, which is
  what the routes call.

## 2. A recompile writes through the one Structure writer

ADR-0014 gave a Game's stages, matches and slots one writer,
`writeStructureTx`. `Recompile` had since grown a second writer of its own,
with the same inserts plus its rules for the bouts it keeps. The two had
already drifted apart: a bout that a recompile added was given no venue.
`createSchemeGame` had also become where Тройка seating was decided. Six of
the last 19 commits to `build.go` were seating rules.

- `writeStructureTx` takes the Game's live Structure (`liveStructure`, nil on
  creation). A stage or bout the scheme still names is rewritten in place.
  A bout that has begun keeps its seats, one that grows takes its new seats
  after its old ones, and any other is reseated. What the scheme no longer
  names is deleted.
- `Recompile` only plans: it loads the live Structure, decides which started
  bouts may grow and refuses the rest (`planGrowthTx`), resolves the venues
  as a clear does, and hands everything to the writer.
- Who a Game created from a DSL seats (a зачёт's troikas, every troika of
  the fest, or empty seats) is `createEntrantsTx` in `seating.go`.

## 3. A write to a fest goes through one commit step

The sequence a write needs was copied by hand, in five different ways: take
the pooled connection before the lock (the 2026-06-13 freeze), run one
transaction, record the revision, drop the cached view, broadcast. The copies
did not agree. `ClearGame` and `DeleteFest` took the raw mutex without
acquiring the connection first. The game settings recorded no revision and
told no open page. The three access writes in `festaccess` took no write
lock at all. The API's access route committed its roles and its hosts' Games
in two separate transactions. The rules for a Game's settings, clear and
delete were methods on `*hostpages.Server`, with their SQL inline.

- `core.Engine.CommitFestWrite(ctx, festID, label, fn)` is the step. `fn`
  returns a `core.FestWrite`: the event the revision records (or the revision
  a domain call already recorded) and `Settled`, which runs after the commit,
  still under the lock. That is where the active-game pointer moves and where
  a reload that no other write may come between is read. `hostpages`'
  `commit` adds the fest-view broadcast, which only the server can build.
- `domain/festops` holds the Game writes (`CreateGameTx`,
  `UpdateSettingsTx`, `ClearGameTx`, `DeleteGameTx`) and `DeleteFestTx`.
  The form handler and its JSON twin both call them.
- The fest's create, settings and access writes, the troika, roster and Flags
  writes, the venues, the start times and the reseed all go through the
  step. The access writes are `festaccess.*Tx` bodies, and the API's twin
  makes its whole change in one transaction.
- Left as they are: the creation form's per-format readers. The JSON twin
  still converts its request to the form's fields so that one parser checks
  both. `pages.Host.Engine()` stays as well, and so do the server's
  `Lock`/`Unlock`, which the Telegram bridge uses.
