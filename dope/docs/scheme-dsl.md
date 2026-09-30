# Scheme DSL

The authoring format for a game's Structure (ADR-0006). Vocabulary — Block, Kind,
Edge, Round, Wave, Lane, Group — is defined in CONTEXT.md; this document is the
grammar and compilation contract.

## Example

An ЭК game (`themes` is ЭК's Protocol param):

```
[defaults]
venues: [Москва-1, Москва-2, Москва-3, Москва-4, Москва-5, Рим]
sorting: [points, h2h, taken, diff]
points: [2, 1, 0]
themes: 6

[init]
seed: kvrm
sorting: [points desc, rating desc]

[scheme]
kind: roundrobin
groups: 8
group_size: 4
proceeding_participants: 2
---
kind: single_elimination
participants: 16
themes: 8
themes.final: 12
reseed: semifinal
venues.final: [Рим]
```

The words are CONTEXT.md's: a Block has a Kind and Participants (a team or a
player — the DSL never says «team»), a Group has a size, a бой a
`match_size`.

Троечка, whose регламент is almost entirely `points`, a scoring rule and
`sorting` (the посев above seats it):

```
[scheme]
kind: roundrobin
groups: 8
group_size: 6
proceeding_participants: 2
themes: 6
metric: total
points: [1, 0.5, 0]
standings.rating: points + taken / 50
sorting: [rating, h2h, taken, diff]
---
kind: single_elimination
participants: 2
bronze: true
best_of.final: 3
best_of.bronze: 3
themes: 6
metric: total
points: [1, 0.5, 0]
standings.rating: points + taken / 20
sorting: [rating]
```

## Grammar

Line syntax is `.hndt`'s (xy `internal/chgk/handout`): one `key: value` per
line, values typed at the accessor (int, string, bracketed list). Lists are
comma-separated: `[points, h2h, taken, diff]`. `#` starts a comment (at
line start or after whitespace). Three `[section]` headers — `[defaults]`,
`[init]`, `[scheme]` — and inside `[scheme]`, blocks separated by lines equal
to `---`, ordered by position.

Sorting items take an optional `asc`/`desc` suffix (`rating desc`); the default
direction is desc (every metric here is better-when-bigger).

Dotted keys scope a value to one Round of the block: `themes.final: 12`,
`title.r3: 1/4 финала`. Every elimination Round answers to `r{N}`, its number
in the block (`r1` is the first round played, whatever its size). A halving
two-seat bracket's last two rounds also answer to `semifinal` and `final`, and
those are their stage codes; `bronze` is the 3rd-place бой. Keys that take a
Round: `title`, `venues`, `best_of`, `match_size` (by `r{N}` only — it shapes
the bracket the words are read from) and every Protocol param. The compiler
rejects a suffix naming a Round the Kind does not generate, and names the
Rounds it does.

## Config cascade

defaults < block < round. `[defaults]` takes `venues`, `sorting`, `points` and
every Protocol param as the game-wide default; the structural keys are a
block's own. Protocol params cascade identically to structure params and are
validated against what the game's Protocol declares
(`protocol.Protocol.Params()`): brain accepts `questions` (always written,
default 5) and `tiebreak_questions`; ЭК, личная СИ and КСИ `themes`; ОД
`tour_comp`. An unregistered key is a compile error naming the keys the block
does take.

## `[init]` — seeding

`seed:` is one token: the reserved words `random` or `xlsx`, else a game slug
in this fest (unknown slug = compile error). Every source yields, per team,
either an exact rank or a basket; baskets resolve to ranks by the deterministic
Жребий lot. Dealing is always the snake: bands of G ranks, odd bands reversed
(the reference generate_kinsbf.py's PF_GROUPS pattern). The same snake deals
reseed ranks into a block's groups. A roundrobin block that must follow a
printed table instead writes it out in `deal:` (below).

- `seed: {game}` — that game's standings as metrics, ordered by `[init]`
  `sorting` (roster rating available as a metric).
- `seed: players` — the посев a сборная format needs, composed over a
  Participant's people rather than over anything it has done itself.
  `games: [a, b]` names the source Games; each player carries `place1..placeN`,
  their own team's place in those Games in that order; `player.<name>: <expr>`
  defines a metric per player in the scoring-rule language; `seed.<name>:
  mean(<player metric>)` folds it over the Participant's players (`mean`,
  `min`, `max`, `sum`, `count`), and `sorting` names those. Who played for whom
  is each source Game's own roster with its overrides applied, so three people
  from three teams are one Троечка team here and their own teams' entries
  there; a player whose team sat a Game out counts one place behind its last.
  `tours.<game>: 1-2` keeps a source ОД to those tours (`2` is the first two,
  `1-2` a range, `[1, 3]` a list): its places are the table after them alone,
  a tie shared at the mean of the places it covers. The Троечка regulations
  seed on the Вопросики after their first two tours: `tours.od-1: 1-2`.
  Teams that share a place share it: two teams at «3–4» are 3.5 each.
  A Participant none of whose people is on the fest roster (stand-ins filling
  the draw) has nothing to add up: it is seeded last, in its order in the
  Game, and the Участники tab names it.
  Participants equal on every `sorting` metric seed by their troikas' order
  of applications (the troikas page's № заявки), then by their order in the
  Game.
- `seed: random` — every rank is a lot.
- `seed: xlsx` — an uploaded sheet carrying either an exact seeding column or a
  basket column.
- `division: Студ` beside a `seed:` keeps the seed to the teams carrying that
  Flag, and `division: -Студ` to those not carrying it (ADR-0020). The student
  and the adult Эрудит-секстет seed from one ОД table this way.
- `division:` with no `seed:` is Тройка's alone: the Game's entrants are the
  fest's troikas in that зачёт (a troika's зачёт is its head team's Flags, or
  the one the host set on the troikas page). They are seated at creation,
  whatever the entrant picker says, and re-seated whenever the troikas page,
  the teams' Flags or the scheme change, until anything is entered in the
  Game. After that its entrants stay as they are.

The DSL only *declares* the source. Resolution is the host pressing the
import button on the game's Посев tab, which snapshots the source's current
standings — partial results mid-fest are a normal seed source — into seed
rows (`imports/seed.go` machinery: sourceRank, decline ticks, ladder,
waitlist). Nothing recomputes afterwards except tick/untick. Supported source
game types: ОД (standings by total, R rating; `[init] sorting` may reorder by
`points`/`rating`) and КСИ; `random` draws a per-game deterministic lot. The
xlsx upload takes column A = team number or name, optional column B = basket
(basket sheets lot within each band; without baskets, row order is the
seeding).

## `[scheme]` — blocks

Block keys — the ones every Kind takes, then each Kind's own. A key the
Kind does not read is a compile error, so nothing is dropped on the floor:

| key | meaning |
|---|---|
| `kind` | `flat`, `roundrobin`, `single_elimination`, `double_elimination` — the DSL words the registered Kinds declare (`structure.Macro`); a Kind is one Go type in `domain/structure` that names its keys (`Keys()`), expands a Block (`Expand`) and ranks its stages (`Ranker`), and adding one is one file, no compiler edit |
| `title` | display title: the stage title of a single-group block, a `{title}. Группа N` prefix on multi-group blocks, a `{title}. {round}` prefix on a bracket's rounds when the scheme has more than one block; `title.r3` names one Round |
| `venues` | restrict the block (or, dotted, one Round) to a venue subset, by title or number |
| `sorting`, `points` | ranking comparators and win/draw/loss values for this block's standings. `points` takes fractions — Троечка pays `[1, 0.5, 0]`, half a балл for a ничья. A metric's direction follows the metric — место and жребий ascend, everything else descends — unless the scheme writes `taken asc`. On a block with `reseed: true` the sorting key describes the Edge instead (groups fall back to `[defaults]`/canon) and names reseed metrics: any metric the game's Protocol writes (`taken` for брейн, `total`/`plus`/`correct_50` for ЭК), plus the reseed's own `place_sum`, `draw` and the КИНСБФ 3.3.5 rates `points_share`, `taken_share`, `taken_base`, `diff` (desc; очки from final outcomes, взятые без перестрелок, разница against opponents in own bouts); default is place_sum, then taken. A group's `sorting` likewise names the Protocol's metrics or what a group adds (`points`, `h2h`, `taken`, `conceded`, `diff`, `place_sum`, `bouts`); anything else is a compile error naming the metrics there are. `h2h` is личная встреча among the tied: at a table of two the points of their bouts against each other, at a table of three or more each pair's places in the бой they shared. `points: [2, 1, 0]` is roundrobin's |
| `reseed` | opt-in re-rank: `true` for the block's incoming Edge (on a DE, between every round too), a round name (`r3`, `semifinal`) for a boundary inside an se block — that round then seats from the re-rank of every place the previous round sent on, bracket-ordered — or `every` for both, the incoming Edge and every se round after it (ТПШ) |
| `stats_from` | with a reseed only: which blocks' bouts the re-rank metrics are summed over (`stats_from: [s1, s2]`); default is the previous block, or the previous round for a boundary reseed. Naming the block itself at a boundary sums its own rounds so far (СтудЧР's ЭК ranked its пересев перед 1/4 by сумма мест over 1/16 and 1/8 together). Eligibility is independent of the stats scope: the previous block's proceeding places, or every place the previous round sent on |
| `proceeding_participants` | block-grain Edge: how many advance per Group (rr, de) or overall (flat); an se sends its last round's winners on |
| `deal` | roundrobin only: which ranks each group takes, when the regulations print a table no snake makes — one item per group, its ranks separated by spaces, every rank of the block once. Своячок's Приложение 1: `deal: [1 12 13 24 28 31 42 43 54, 2 11 14 23 25 32 41 44 53, …]`. Ranks are the seed's on the first block and the reseed's after `reseed: true` |
| `letters` | `letters: false` keeps the block's бои out of the буква deal — the письменный отбор is one sitting for everyone and is not called a бой |
| `bout.<metric>`, `standings.<metric>` | scoring rules (ADR-0008): an expression per бой summed into the standings, or one over the sums, defining a metric the block may sort by |
| flat: `participants` | how many the one бой seats (defaults to the game's entrants) |
| rr: `metric` | the Protocol metric забито counts, when it is not `taken` — Тройка's is `total`, its игровые очки |
| rr: `groups`, `group_size`, `match_size` | the shape; each Group is one ranking scope. `match_size` above 2 is a Group playing бои of three or four (личная СИ) |
| rr: `rounds` | play this many круги and stop |
| rr: `slug` | the URL slug of the group stage's tabs |
| se: `participants`, `match_size`, `winning_places` | the draw size, the seats per бой (`match_size.r3` for one Round — ЭК plays its 1/4 three to a table), and how many of a бой's places count as winning it: those go on, the rest take their Loss (CONTEXT.md «Winning places»). Two-seat, one winner by default; ЭК's `participants: 48, match_size: 4, winning_places: 2` is the five rounds 1/16, 1/8, 1/4, полуфиналы, финал; without `winning_places` it would be 48→12→3 |
| se: `rounds` | play this many Rounds and stop short of a final (ТПШ's six winners); the last round's winners are the block's proceeding count |
| se: `bronze` | add the 3rd-place бой from the semifinal losers (a halving bracket that reaches its semifinal). With `participants: 2` there is no semifinal to lose: the block is seeded straight into its final, takes four out of the previous block's two Groups, and the матч за 3-е место seats the place below the finalists — Троечка's «победители групп в финал, вторые места в матч за 3-е место» |
| se: `best_of` | the final and the бронза (`best_of.final: 3`, `best_of.bronze: 3`, odd ≥ 3): that Round becomes a series of identical бои at one стол. A series is a ranking scope like a группа — it ranks through the block's `points`, scoring rules and `sorting`, so the default очки sorted on points is «до большинства побед» and Троечка writes its own rule instead. No block can follow a series |
| se: `pairing` | how a Round takes the previous Round's бои: `adjacent` (default) pairs бой 1 with 2, 3 with 4; `halves` pairs бой 1 with N/2+1, 2 with N/2+2 — the Троечка regulations' 1/8 (R1 = Q1–Q9, R2 = Q2–Q10), one бой of either заход. `pairing.r2: halves` for one Round; a plain `pairing: halves` pairs every Round that has a previous one without a reseed |
| se: `rollout` | draw a series as its бои rather than rank it: `rollout: true` for the block, `rollout.final` / `rollout.bronze` for one Round. The Сетка then shows the three boxes it showed before, and nothing computes who won the series — which is what a tournament that reads its финал off the бои themselves wants (СтудЧР's брейн) |
| se: `draw` | `draw: true` makes the host draw the opening round on the day (Троечка §5.3). The bracket must follow groups sending two places each into bouts of two, with `participants` twice the number of groups. Each bout then has two Draw Slots: one offers every group's first place, the other every group's second place, both once the groups are played out. The host fills them on the Сетка's Жеребьёвка panel, and dope refuses to put a winner and a runner-up of the same group in one bout. The rounds after it pair bout 1 with bout 2, 3 with 4, and so on, unless `pairing` says otherwise |
| de: `groups`, `group_size`, `participants`, `match_size`, `winning_places` | pods of the given size (`participants` ÷ 4 may stand in for `groups`), each a ranking scope on two lives; `match_size` and `winning_places` as for se (личная СИ's play-off is one DE of four-seat бои with two winners) |
| de: `lower_entrants` | how many of the bracket's entrants — the ranking's tail — start one Loss down, in the lower bracket. Octobearfest's личная СИ (Приложение 3) seats a group's 1st and 2nd places above and its 3rd and 4th below: `participants: 24`, `lower_entrants: 12` and the `opening:` table below play 6 + 5 + 3 + 2 + 1 + 1 бои, the upper bracket's second round and the lower semifinal three to a table. A re-rank of all 24 (`reseed: true` alone) cannot make that split: it ranks by the sorting over every group's bouts, and a strong group's 3rd place can outrank another group's 2nd. Default 0: everyone starts with two lives |
| de: `opening` | the opening round's bouts as the regulations print them, instead of a re-rank of the previous block: one item per bout, its seats as the previous block's group letter (A is the first group) and place — Приложение 3: `opening: [A1 A2 D1 D2, B1 B2 E1 E2, C1 C2 F1 F2, A3 A4 D3 D4, B3 B4 E3 E4, C3 C4 F3 F4]`. The upper bracket's bouts come first, then the lower's (`lower_entrants`). Every place the previous block sends on is named once. `reseed: true` beside it re-ranks between the later rounds only |
| protocol keys | any registered param for the game's protocol |

Advancement between and inside blocks is deterministic by the Kind's canonical
templates (bracket order for eliminations, КИНСБФ cross templates between
blocks — taken from google_sheet_writer's generate_kinsbf.py conventions) so
participants change venues as little as possible. `reseed` replaces the template at
that one boundary with a global re-rank by `sorting`. The re-rank resolves like
a seed import: the host presses «Рассчитать» on the reseed panel (Протоколы
tab) once every source бой is finished; the ranks then seat the next block.

Blocks chain linearly; non-proceeding participants end with final classification from
the standings they finished with. Consolation play-offs are a planned Edge
extension (`from: block 2 ranks 17-32`), not v1.

## `[defaults]`-only keys

`venues:` — either a count (`venues: 6`, auto-titled) or a titled list; absent,
the count is derived as the widest block's lane need. The compiler splits a
Round with more matches than venues into ⌈matches/venues⌉ Waves (`-w{k}` stage
codes) in canonical order and maps rr Groups one per venue, cycling. Concrete
venues stay editable after compilation through the existing venues API; DSL
names are initial values.

## Compilation

The compiler expands blocks into the existing detailed scheme JSON
(`store.FestScheme`) and model rows — `rr` stages per Group, `matches` stages
per Wave for eliminations, `reseed` rows for reseed Edges. Codes are
deterministic and hyphenated (stage `s1-g2`, `s2-semifinal`, `s2-r1-w2`;
match `s2-semifinal-m1`), so recompiling an edited DSL preserves every
surviving match's identity (state, journal, SSE scopes). A recompile touches
only what has not started: pristine бои rebuild freely (questions changes,
added blocks, deletions), while a бой with entered marks must survive with
identical slot sources — otherwise the whole edit is refused, naming the
offending бои.

Deterministic advancement templates: pods (paired groups) fill opposite se
bracket halves — winners' matches first, runner-up-led rematches in the second
half — so pod survivors only meet again in the late rounds; DE groups draw
row-wise from source groups (place 1 of own column, place 2 of the partner
column, per wave-row) as in the reference sheets.

Hand-authoring or importing detailed JSON remains possible and detaches the
game from its DSL; that path is also the only way to a `manual` (hand-
enumerated) block, which has no DSL spelling.

## UI

`games.scheme_dsl` column; the game creation/settings page shows a monospace
textarea prefilled with a per-game-type template. Compile errors block the save
and render with line numbers. Clearing a pre-DSL brain game first re-expresses
its shortcut scheme in the DSL, upgrading it onto the one authoring path. A
future point-and-click builder reads and writes the same column.
