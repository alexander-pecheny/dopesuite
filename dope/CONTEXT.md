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
One scheme element: one Kind plus its config. The unit of composition a scheme author thinks in. A flat game (ЧГК, КСИ) is a degenerate Structure: one `flat` Block, one Match seating everyone.
_Avoid_: Stage — the retired term (and legacy DB name); half its uses meant Block, half Round.

**Kind**:
A registered macroexpansion algorithm that turns a Block's config into Rounds of Matches and defines how the Block's Participants are ranked: `flat`, `roundrobin`, `single_elimination`, `double_elimination`, `swiss`. One more Kind exists only below the DSL: `manual` — hand-enumerated pairings, the compiled form of imported or hand-authored schemes (chr2026's EK bracket), never a DSL word.

The two elimination Kinds are told apart by how many Losses end a Participant's tournament — one, or two — and by nothing else. Neither implies a Match of two seats nor a single survivor: ЭК plays its bracket four to a table with two proceeding, and личная СИ's entire play-off is a double elimination of four-seat бои. A Block ranks Participants who never met by how far they got and how they placed on the way out.

**Loss (Поражение)**:
Failing to be among a Match's Winning places. It is the only currency the elimination Kinds count — a Participant leaves the Block on its first or second one.

**Winning places**:
How many of a Match's places count as winning it, so the rest take a Loss. One in a two-seat бой; two at ЭК's and личная СИ's tables of four («места 1–2 считаются победой»). Distinct from a Block's proceeding count, which says how many Participants leave the Block for the next one — a КИнСБФ pod of four stops as soon as its two qualifiers exist, while личная СИ plays on to a champion.

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

**Draw (Жеребьёвка)**:
A seating no result implies: the initial deal into Groups, ЭК's hand-drawn bracket, a table swapped on the day for a no-show. A Draw is *input* to a Structure and is written into the Edges that fill those Slots, as against derived seating, which the Structure computes from prior results and recomputes whenever they change. A hand-placed derived seat is not a Draw — it is a seat the Structure will overwrite, correctly.
_Avoid_: recording an observed seating as fact without deciding which it is. If the Structure should have produced it, a mismatch is a defect; if nothing could have produced it, it is a Draw.

**Scheme**:
The declarative document describing a Game's Structure — its Blocks, Edges, and Slot sources. The source of truth for authoring: hand-written, imported, or emitted from the simplified scheme DSL.

**Match**:
One sitting of Participants scored together under one Protocol. The unit the Structure schedules and the unit a host edits.
_Avoid_: bout as a distinct concept — бой is just the brain-format word for a Match.

**Protocol**:
The in-match ruleset: state shape, scoring, and rendering for what happens inside one Match (EK's 12 themes, КСИ's grid, ЧГК's question grid via the `od` protocol; brain's K buzzer questions once it ships). Registered once; the Structure only consumes its output (place + metrics per Slot). Protocol params may vary across one Game (6 themes in early Rounds, 12 in the final): a Game carries defaults plus per-Block or per-Round overrides.

**Metric**:
One named number attached to a Participant. A Protocol declares and emits the ones it can measure inside a Match (взятые, Σ, Σ+, взятые за 50); the Structure derives the rest (place, очки, сумма мест, Losses). Every ranking rule — a Block's sorting, a reseed Edge's order — names Metrics, and a Scoring rule can define new ones. A Protocol that starts measuring something new makes it rankable everywhere by declaring it.

**Scoring rule**:
An arithmetic expression a scheme author writes to derive a Metric, at one of two grains: per Match, evaluated for each Participant over that бой's outcome and summed into the standings (`4 − место`); or per standings, evaluated once over those sums (`очки / (2 × бои)`). The grain matters — summing a per-Match rule is not the same as evaluating it on the sums, which is why both exist.

**Slot**:
One seat in a Match. Declares where its occupant comes from (a seed, or an Edge — a place in a prior Match, a rank in a standings) and who currently sits in it.

**Participant**:
Whoever occupies a Slot and is scored: a team in team formats, one player in individual ones (личная СИ). It is the identity every Match result, standing and Edge is keyed on, and it points back at the Fest roster entry it was drawn from — either a team or a player, recorded as its `roster` (the word Kind is spoken for; see below).
_Avoid_: calling a Participant a team. A team is one of the two things a Participant can be, not the general word for it.

**Number**:
What a Participant is called by in one Game — the number a host announces and a protocol sheet prints, dealt from 1 within that Game. The same team carries different numbers in different Games of one Fest (ЭК numbers its 48 entrants, ОД its 65), so a Number belongs to a Participant's entry in a Game. The Fest registry holds identity — name, city, players — and never a playing number.

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
