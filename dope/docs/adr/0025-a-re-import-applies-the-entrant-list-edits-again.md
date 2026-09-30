---
status: accepted
date: 2026-09-30
---

# A re-import applies the entrant list's hand edits again

An entrant list (ADR-0023) is imported from a source and then edited by hand.
Until now a re-import threw the edits away, after asking. That was the usual
way to lose work: a host seeded an ЭС from the ОД, took out a team that
withdrew, added a guest team and moved one up, then the ОД's results changed
and the seeding had to be taken again. The fest roster had the same problem,
and ADR-0024 settled it by keeping the host's edits apart from what they
change. Entrant lists now do the same.

## Decision

The list keeps, beside its rows, the host's hand edits since the last import,
in order (`seedImport.edits` in the Game's state): an entrant added, taken out,
or moved to a place. A re-import builds the list from the source as before,
then applies the edits to it again (`imports.Replay`). An added entrant comes
back at the end, one taken out stays out, one moved goes to the same place.
An edit about an entrant the new list does not have, or already has, changes
nothing. Declines are kept as before, and an added entrant keeps its own.

The log folds what cancels: taking out an entrant the host had added forgets
both, and a second move of an entrant replaces the first. Renaming a one-off
renames the entrant itself, so it is not logged.

The Участники tab no longer asks before a re-import. It shows «Сохранить мои
правки (N)», ticked; clearing it imports the source's list alone
(`Source.Fresh`), which asks first because it drops the edits. A Тройка that
follows its зачёт follows again only after such a fresh import: a plain one
keeps the host's edits, so the list stays the host's.

## Why

The source is right about the order, and the host is right about who plays.
Replaying the host's edits keeps both. A move is kept as a place in the list
(«третьим»), not as «above this team», because that is how a host says it and
it stays meaningful whatever the source's order becomes.

## Amendment: an import from another source keeps no moves

A move is a place in the order one source gave. An import from another
source — another kind, another Game or another division — orders by
something else: a Троечка's list is taken from its troikas in application
order and then from `seed: players`. Replaying «third» there undid the
seeding without a word (the Octobearfest rehearsal). Such an import now
replays the additions and removals and leaves the moves out; the tab says how
many (`movesDropped`). A re-import from the same source replays everything as
before.
