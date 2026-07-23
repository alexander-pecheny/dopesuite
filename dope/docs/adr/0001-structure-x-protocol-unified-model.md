---
status: accepted
date: 2026-07-23
---

# Every game is Structure × Protocol, and everything migrates onto it

Dope grew two disjoint shapes — EK as relational bracket (`stages/matches/match_slots/themes/answers`) and OD/KSI as opaque JSON blobs on `games` — and the first attempt at a third format (brain) added a third bespoke shape. With 6+ more formats planned (brain, individual SI, troika, media games), we decided every game is one **Structure** (a composition of registered Stage Kinds that schedules Matches, seats Participants into Slots, and advances them) whose Matches each run one registered **Protocol** (state shape + scorer + renderer). Flat games are the degenerate Structure "one stage, one match". EK/OD/KSI are rebuilt onto this model rather than coexisting with it — one shape in the codebase before any new game ships.

## Consequences

- Participants are polymorphic: a Game declares team or player kind; slots/results reference `(kind, id)` against the existing `fest_teams`/`fest_players` registries.
- Stage Kinds separate schedule production (possibly incremental — swiss; possibly hand-authored pairings — partial round-robins) from standings computation, so new primitives register without schema changes.
- Match places are computed by the Protocol scorer with host override; only overrides are inputs, computed places are derived state.
- v1 implements round-robin, single-elim, and reseed; the schema is born ready for swiss (mid-stage match creation) and double-elim.
- Structures are authored as declarative scheme documents plus parameterized generators; a visual builder can come later on top of the same document.
