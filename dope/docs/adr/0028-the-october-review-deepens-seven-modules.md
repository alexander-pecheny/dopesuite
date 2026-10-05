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
