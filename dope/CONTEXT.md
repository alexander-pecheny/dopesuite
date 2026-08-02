# Dope — tournament scoring

Live scorekeeping for Russian-language trivia formats: a snappier replacement for the community's Google Sheets. Every game is a **Structure × Protocol** composition (decided 2026-07-23; supersedes the old EK-relational / ChGK-JSON-blob split — all formats migrate onto the unified model).

## Language

**Fest**:
One real-world event: a dated gathering with a team registry, hosts, and one or more Games.

**Game**:
One competition inside a Fest, played to completion under a single format (e.g. the ЧГК game, the brain bracket). A Game = one Structure whose Matches all run Protocols.

**Structure**:
The bracket of a Game: Blocks connected by Edges, creating Matches, seating Participants into their Slots, and advancing them. Game-agnostic — it never knows Protocol rules, only per-Slot outcomes.

**Block**:
One scheme element: one Kind plus its config. The unit of composition a scheme author thinks in. A flat game (ЧГК, КСИ) is a degenerate Structure: one manual Block, one Match seating everyone.
_Avoid_: Stage — the retired term (and legacy DB name); half its uses meant Block, half Round.

**Kind**:
A registered macroexpansion algorithm that turns a Block's config into Rounds of Matches and defines how the Block's Participants are ranked: `roundrobin`, `single_elimination`, `double_elimination`, `swiss`. One more Kind exists only below the DSL: `manual` — hand-enumerated pairings, the compiled form of imported or hand-authored schemes (chr2026's EK bracket), never a DSL word.

**Edge**:
An advancement rule connecting outcomes to future seats. Match-grain: place N in Match X fills a Slot (how brackets chain). Block-grain: rank N of a standings computed over source Blocks or Groups fills a Slot — carrying the proceeding count, sorting comparators, and tie lots.

**Round (Этап)**:
One dependency layer of a Block's expansion: a set of Matches with no ordering dependencies among themselves (single elimination of 48 → five Rounds; a round-robin's circle-rounds are Rounds too, degenerate when a Group sits at one table).

**Wave (Заход)**:
The venue-constrained realization of a Round: a set of Lanes running in parallel. A Round with more Matches than venues splits into several Waves. The Russian «заход» is only said aloud when there is more than one.

**Lane (Дорожка)**:
One venue's ordered string of Matches within a Wave. In one-sitting formats a Lane is a single Match; a round-robin Group playing at one table is a Lane of all that Group's Matches.

**Group**:
One ranking scope inside a round-robin Block: the Participants who all play each other and are ranked together. A Block may hold many Groups (групповой этап of 8 groups = one Block).

**Scheme**:
The declarative document describing a Game's Structure — its Blocks, Edges, and Slot sources. The source of truth for authoring: hand-written, imported, or emitted from the simplified scheme DSL.

**Match**:
One sitting of Participants scored together under one Protocol. The unit the Structure schedules and the unit a host edits.
_Avoid_: bout as a distinct concept — бой is just the brain-format word for a Match.

**Protocol**:
The in-match ruleset: state shape, scoring, and rendering for what happens inside one Match (EK's 12 themes, КСИ's grid, ЧГК's question grid via the `od` protocol; brain's K buzzer questions once it ships). Registered once; the Structure only consumes its output (place + metrics per Slot). Protocol params may vary across one Game (6 themes in early Rounds, 12 in the final): a Game carries defaults plus per-Block or per-Round overrides.

**Slot**:
One seat in a Match. Declares where its occupant comes from (a seed, or an Edge — a place in a prior Match, a rank in a standings) and who currently sits in it.

**Participant**:
Whoever occupies a Slot — a team in team formats, an individual player in individual formats (individual СИ).

**Numbering guard**:
Every Participant needs a number before results can be entered — the number is the identity every format scores against, so entering results before they exist would attach data to an unstable key. The server refuses writes while any is missing (409), and a game page shows the guard's message in place of its input sheet, naming the teams still without one.

**Pin**:
A host's manual place assignment for a Slot, part of the Match's Protocol state. A Pin beats the scorer's computed place at every recompute until the host clears it.

**Reseed**:
The block-grain Edge's computation: re-ranking Participants from prior results (with deterministic lots for true ties) so a later Block can seat by rank. Conceptually an Edge rule, not a Block, however it is persisted.

**Перестрелка**:
A tiebreak continuation, common to every format (EK's shootout themes, ОД's shootout rounds, brain's "П" questions). Two distinct forms: extra material appended to the Match itself until the tie breaks, or a separate replay Match between fully tied Participants. Whether a Block's Matches allow the appended form is part of that Block's rules — regulations differ per tournament.

**Личная встреча**:
The head-to-head comparator in group ranking: among Participants tied on очки, the points they took in Matches between themselves (for exactly two, simply who won their Match). Ranking comparators and their order are per-Block rules.
