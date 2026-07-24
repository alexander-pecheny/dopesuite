# Dope — tournament scoring

Live scorekeeping for Russian-language trivia formats: a snappier replacement for the community's Google Sheets. Every game is a **Structure × Protocol** composition (decided 2026-07-23; supersedes the old EK-relational / ChGK-JSON-blob split — all formats migrate onto the unified model).

## Language

**Fest**:
One real-world event: a dated gathering with a team registry, hosts, and one or more Games.

**Game**:
One competition inside a Fest, played to completion under a single format (e.g. the ЧГК game, the brain bracket). A Game = one Structure whose Matches all run Protocols.

**Structure**:
The bracket of a Game: an ordered composition of Stages that creates Matches, seats Participants into their Slots, and advances them between Stages. Game-agnostic — it never knows Protocol rules, only per-Slot outcomes.

**Stage**:
One instance of a Stage Kind inside a Structure. A flat game (ЧГК, КСИ) is a degenerate Structure: one Stage, one Match seating everyone.

**Stage Kind**:
A registered structural primitive — today round-robin (`rr`), single-elimination (`se`), reseed, and manual hand-authored pairings (`matches`); swiss and double-elim are planned, the schema is born ready for them. A Stage Kind owns two separable concerns: producing the Stage's Match schedule (possibly incrementally, possibly from hand-authored pairings) and ranking the Stage's Participants from Match outcomes.

**Scheme**:
The declarative document describing a Game's Structure — its Stages, Matches, and Slot sources. The source of truth for authoring: hand-written, imported, or emitted by a parameterized generator.

**Match**:
One sitting of Participants scored together under one Protocol. The unit the Structure schedules and the unit a host edits.
_Avoid_: bout as a distinct concept — бой is just the brain-format word for a Match.

**Protocol**:
The in-match ruleset: state shape, scoring, and rendering for what happens inside one Match (EK's 12 themes, КСИ's grid, ЧГК's question grid via the `od` protocol; brain's K buzzer questions once it ships). Registered once; the Structure only consumes its output (place + metrics per Slot).

**Slot**:
One seat in a Match. Declares where its occupant comes from (seed, a place in a prior Match, a rank in a Stage or reseed) and who currently sits in it.

**Participant**:
Whoever occupies a Slot — a team in team formats, an individual player in individual formats (individual СИ).

**Pin**:
A host's manual place assignment for a Slot, part of the Match's Protocol state. A Pin beats the scorer's computed place at every recompute until the host clears it.

**Reseed**:
A Stage that re-ranks Participants from prior results (with deterministic lots for true ties) so later Stages can seat by rank.
