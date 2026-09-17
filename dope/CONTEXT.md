# Dope — tournament scoring

Live scorekeeping for Russian-language trivia formats, meant to be a faster
replacement for the Google Sheets the community uses. Every game is built as a
**Structure combined with a Protocol**. That was decided on 2026-07-23 and
replaces the older split, where EK was relational and ChGK was a JSON blob. All
formats are being moved onto the one model.

## Language

**Fest**:
One real event: a gathering on a given date, with a registry of teams, hosts, and one or more Games.

**Game**:
One competition inside a Fest, played from start to finish under a single format, such as the ЧГК game or the brain bracket. A Game is one Structure, and all of its Matches run Protocols.

**Structure**:
The bracket of a Game. It is made of Blocks connected by Edges, and it creates the Matches, seats Participants into their Slots and advances them. It knows nothing about any format: it never sees a Protocol's rules, only the outcome of each Slot.

**Block**:
One element of a scheme: one Kind, plus the configuration for it. This is the unit a person writing a scheme thinks in. A flat game such as ЧГК or КСИ is the simplest possible Structure: a single `flat` Block with one Match that seats everybody.
_Avoid_: Stage. That is the retired term, and it is still the name in the database. It was ambiguous: half its uses meant Block and half meant Round.

**Kind**:
A registered macro-expansion algorithm. It turns a Block's configuration into Rounds of Matches, and it also defines how the Participants in that Block are ranked. The Kinds are `flat`, `roundrobin`, `single_elimination`, `double_elimination` and `swiss`. There is one more, `manual`, which exists only below the DSL: it is hand-enumerated pairings, and it is what an imported or hand-written scheme compiles into, such as chr2026's EK bracket. It is never a word you can write in the DSL.

The only difference between the two elimination Kinds is how many Losses end a Participant's tournament: one, or two. Neither of them implies that a Match has two seats, nor that there is a single survivor. ЭК plays its bracket four to a table with two of them going through, and личная СИ's entire play-off is a double elimination of бои with four seats. Where two Participants in a Block never played each other, the Block ranks them by how far they got and how they placed on the way out.

**Loss (Поражение)**:
Failing to finish a Match in one of its Winning places. This is the only thing the elimination Kinds count. A Participant leaves the Block on their first Loss, or their second.

**Winning places**:
How many of a Match's places count as winning it. Everyone below them takes a Loss. In a two-seat бой it is one; at ЭК's and личная СИ's tables of four it is two («места 1–2 считаются победой»). This is not the same as a Block's proceeding count, which says how many Participants leave the Block for the next one. A КИнСБФ pod of four stops as soon as it has produced its two qualifiers, while личная СИ keeps playing until there is a champion.

**Edge**:
A rule that connects an outcome to a future seat. There are two grains. At Match grain, place N in Match X fills a Slot, which is how brackets chain together. At Block grain, rank N of a standings computed over some source Blocks or Groups fills a Slot; that form also carries the proceeding count, the sorting comparators and the tie lots.

**Round (Этап)**:
One dependency layer of a Block's expansion: a set of Matches that do not depend on each other and can therefore be played in any order among themselves. A single elimination of 48 expands into five Rounds. A round-robin's circle-rounds are Rounds too, though they collapse to one when a Group sits at a single table.

**Wave (Заход)**:
What a Round becomes once the number of venues is taken into account: a set of Lanes running at the same time. If a Round has more Matches than there are venues, it is split into several Waves. In Russian, «заход» is only said out loud when there is more than one.

A Wave is usually a stage in its own right. A round-robin Group is an exception, because it is a Lane: it occupies one table from its first Match to its last. Its stage therefore spans every Round and every Wave, and both coordinates are stored on the Match. Личная СИ's Group of nine plays its круг three players at a table, one group after another: that is three Waves, not three tables.

**Lane (Дорожка)**:
The ordered run of Matches at one venue within a Wave. In a format played in one sitting, a Lane is a single Match. A round-robin Group playing at one table is a Lane containing all of that Group's Matches.

**Group**:
One ranking scope inside a Block, meaning the set of Participants who are ranked together. That can be a round-robin группа where everyone plays everyone, or an elimination pod, as in брейн's double elimination of six pods of four. A Block may contain many Groups: a групповой этап of eight groups is one Block. A Group is also a row of the Сетка — Group k of every Block sits in row k, and that is what makes the columns line up.
_Avoid_: treating a pod as a separate concept. It is just the double-elimination word for a Group.

**Draw (Жеребьёвка)**:
A seating that no result implies: the initial deal into Groups, ЭК's hand-drawn bracket, or a table swapped on the day because somebody did not turn up. A Draw is an *input* to a Structure, and it is written into the Edges that fill those Slots. Derived seating is the opposite: the Structure works it out from earlier results and recalculates it whenever they change. A seat that somebody placed by hand but that the Structure derives is not a Draw; it is a seat the Structure will overwrite, and it is right to do so.
_Avoid_: recording a seating you have observed without deciding which of the two it is. If the Structure should have produced it, then a mismatch is a bug. If nothing could have produced it, then it is a Draw.

**Scheme**:
The document that describes a Game's Structure: its Blocks, its Edges and where each Slot's occupant comes from. It is the source of truth for authoring, and it can be written by hand, imported, or generated from the simplified scheme DSL.

**Сетка**:
The map of a whole Game at a glance. It shows each Block's standings at the coarsest useful level of detail — place, and at most a total — and it is the single place where anyone playing can find out who goes where next. It is deliberately compact: crosstabs, protocols and per-Metric detail belong to a Block's own views and never to the Сетка. Its rows are shared across columns the way a spreadsheet's are: a Round is a column, a Group is a row, and a Block containing several Groups wraps into as many columns as the height of the screen requires.

**Буква боя**:
The handle people use for a Match. Each Game has its own sequence of letters — A to Z, then AA and onwards — dealt by the compiler in schedule order, which is Block, then Round, then Match, and stored on the Match. A Block may decline to use letters at all; ТПШ's письменный отбор is one sitting rather than a бой. The letter is what a URL and a page show. The structural code, such as `s1-r2-m3`, remains the stored identity underneath, so editing a scheme in a way that renumbers the letters moves the links but never the results.

**Match**:
One sitting of Participants who are scored together under one Protocol. It is the unit the Structure schedules, and the unit a host edits.
_Avoid_: treating a bout as a separate concept. «Бой» is simply the brain-format word for a Match.

**Ranking scope**:
Any set of Participants who are ranked together. There is one procedure for ranking such a set: take the block's `points` for each бой's outcome, add up the Protocol's metrics, apply the scheme's Scoring rules to those sums, and then order by the scheme's sorting comparators. A round-robin Group is a ranking scope, and so is a Round in which the same two Participants play several times. That is why «до большинства побед» is not a rule written into the code: it is just the default очки, sorted on очки. It is also why Троечка's «победа в первых двух боях не гарантирует общую победу» takes three lines of scheme.

**Series**:
A Round played more than once by the same two Participants: a финал до большинства побед, or Троечка's финал of three боёв. It is a ranking scope rather than a step in a bracket, so what it decides comes out of its own standings. A series played to a summed рейтинговый балл and one played to two wins differ only in the scheme they carry.

**Protocol**:
The rules inside a Match: the shape of its state, how it is scored, and how it is rendered. That covers EK's 12 themes, КСИ's grid, and ЧГК's question grid through the `od` protocol, and it will cover brain's K buzzer questions when that ships. A Protocol is registered once, and the Structure only consumes what it produces, which is a place and a set of metrics for each Slot. A Protocol's parameters may differ within a single Game — six themes in the early Rounds and twelve in the final — so a Game carries defaults plus overrides per Block or per Round.

**Metric**:
One named number attached to a Participant. A Protocol declares and emits the ones it can measure inside a Match: взятые, Σ, Σ+, взятые за 50. The Structure derives the rest: place, очки, сумма мест, Losses. Every ranking rule names Metrics, whether it is a Block's sorting or the order on a reseed Edge, and a Scoring rule can define new ones. If a Protocol starts measuring something new, declaring it is enough to make it rankable everywhere.

**Scoring rule**:
An arithmetic expression that a scheme author writes in order to derive a Metric. There are two grains. A per-Match rule is evaluated for each Participant over that бой's outcome, and the results are summed into the standings; `4 − место` is one. A per-standings rule is evaluated once, over those sums; `очки / (2 × бои)` is one. Both exist because the grain changes the answer: summing a per-Match rule is not the same as evaluating that rule on the sums.

**Slot**:
One seat in a Match. It declares where its occupant comes from, which is either a seed or an Edge — a place in an earlier Match, or a rank in a standings — and it records who is sitting in it now.

**Participant**:
Whoever occupies a Slot and is scored. In a team format that is a team, and in an individual one such as личная СИ it is a single player. Every Match result, standing and Edge is keyed on this identity, and it points back at the entry in the Fest roster it came from, which is either a team or a player. Which of the two it is recorded in its `roster` field, because the word Kind is already used for something else, as described above.
_Avoid_: calling a Participant a team. A team is one of the two things a Participant can be, not the general word for it.

**Number**:
What a Participant is called within one Game: the number a host announces and a protocol sheet prints, counted from 1 inside that Game. The same team has different numbers in different Games of the same Fest, since ЭК numbers its 48 entrants and ОД numbers its 65, so a Number belongs to a Participant's entry in a Game. The Fest registry holds identity — name, city, players — and never a playing number.

**Numbering guard**:
Every Participant needs a number before any result can be entered, because the number is the identity every format scores against, and entering results first would attach the data to a key that is still moving. The server refuses writes with a 409 while any number is missing, and a game page shows the guard's message instead of its input sheet, naming the teams that still have none.

**Pin**:
A place that a host has set by hand for a Slot. It is part of the Match's Protocol state, and it wins over the place the scorer computes, at every recalculation, until the host clears it.

**Reseed**:
What a Block-grain Edge computes: it re-ranks Participants from earlier results, breaking genuine ties with deterministic lots, so that a later Block can seat people by rank. It is a rule on an Edge, whatever way it happens to be stored.

**Мини-игра**:
One of the several small games that make up a Мультиигры sitting. It is a раздаточный конкурс with its own tasks, and everyone plays it at the same time. Each task is declared with a domain: the set or range of values that task's cell may hold. That one declaration serves as the validation, as the номинал printed on the sheet, and as the editor for the cell. Every мини-игра produces a subtotal, and the Итог is the sum of those. A мини-игра's tasks may be arranged in блоки, which are visually separate groups on its sheet; the task numbering still runs straight through the whole мини-игра.

A мини-игра can instead be **normalised**, written `→0..100`. It then contributes what the team scored as a share of the best score in it, out of a hundred, rather than its own points. That is what lets мини-игры of very different sizes count for the same amount: «Ассорти» combined a медиа-эрудит worth 980 with a песенный конкурс worth 55. The best score is taken from the teams that played it, because a team that refused ([[Отказ]]) should not set the scale for everyone else. A team that ends a мини-игра on a negative score gets nothing for it, rather than dragging its own Итог down.

**Кресло**:
One of the three seats at a Троечка table, numbered in the order the ведущий asks them: two пристяжные, and then the коренной, who signals. All three answer every вопрос their team plays, and each correct answer scores on its own, so what a кресло records is a mark and never a rank. Who sits in a кресло is a fact about the тема rather than about the бой, because the регламент has the пристяжные turn round at the половина and teams often swap more than that. It is also what distinguishes a first correct answer from a repeat of one that is already on the table.

**Рассадка**:
The order of a side's three кресла for one тема: who is first пристяжной, who is second, and who is коренной. A side's рассадка stays in force from the тема where it is set until it is set again. A тема whose рассадка differs from the one before it is where that side turned round.

**Зачёт**:
A standings that a team competes in. One tournament may run several зачёты at once on the same questions. At an Открытый чемпионат Польши, Polish teams compete for the national medals while every team competes for the festival's. «Вне зачёта» is itself a зачёт: usually adult teams playing a school or student championship for fun. Source sheets normally mark a team's зачёт in a column of its own. Each зачёт ranks its own teams, and вне-зачёта teams are shown below the ranked ones, in full and not greyed out, so that they inform without getting in the way. This is not modelled yet: at the moment a game ranks everybody in a single зачёт, and marking a team out of it is not an [[Отказ]].

**Отказ**:
A team refusing to play a Game or a мини-игра. The team keeps its row, so that the numbers of the other teams do not shift, but it has no results there by definition: it takes no place and it cannot set a normalisation scale. This is not the same as «вне зачёта» ([[Зачёт]]), where a team plays in full and appears alongside everyone else, and is only outside the medal standings.

**Перестрелка**:
A tiebreak continuation. Every format has one: EK's shootout themes, ОД's shootout rounds, and brain's "П" questions. It takes two forms. Either extra material is added to the Match itself until the tie is broken, or a separate replay Match is played between the Participants who are exactly level. Whether a Block's Matches allow the first form is part of that Block's rules, because the regulations differ from tournament to tournament.

**Личная встреча**:
The head-to-head comparator used when ranking a group. Among Participants who are level on очки, it compares the points they took in the Matches they played against each other. When exactly two are level, that is simply whoever won their Match. Which comparators are used, and in what order, is decided per Block.
