# Scheme DSL

The authoring format for a game's Structure (ADR-0006). Vocabulary — Block, Kind,
Edge, Round, Wave, Lane, Group — is defined in CONTEXT.md; this document is the
grammar and compilation contract.

## Example

```
[defaults]
venues: [Москва-1, Москва-2, Москва-3, Москва-4, Москва-5, Рим]
sorting: [points, head2head, taken, diff]
points: [2, 1, 0]
themes: 6

[init]
seed: kvrm
sorting: [points desc, rating desc]

[scheme]
type: roundrobin
groups: 8
teams_in_group: 4
proceeding_teams: 2
---
type: single_elimination
teams: 16
themes: 8
themes.final: 12
reseed: r4
venues.final: [Рим]
```

## Grammar

Line syntax is `.hndt`'s (xy `internal/chgk/handout`): one `key: value` per
line, values typed at the accessor (int, string, bracketed list). Lists are
comma-separated: `[points, head2head, taken, diff]`. `#` starts a comment (at
line start or after whitespace). Three `[section]` headers — `[defaults]`,
`[init]`, `[scheme]` — and inside `[scheme]`, blocks separated by lines equal
to `---`, ordered by position.

Sorting items take an optional `asc`/`desc` suffix (`rating desc`); the default
direction is desc (every metric here is better-when-bigger).

Dotted keys scope a value to one Round of the block by its canonical code:
`themes.final: 12`. Elimination rounds answer to their canonical code
(`r16`, `semifinal`, `final`, `bronze`) and the `r{N}` alias (`r4` ≡
`semifinal`, `r2` ≡ `final`). The compiler rejects a suffix naming a Round the
Kind does not generate.

## Config cascade

defaults < block < round. Any key legal in a block may sit in `[defaults]` as
the game-wide default. Protocol params cascade identically to structure params
and are validated against what the game's Protocol declares
(`protocol.Protocol.Params()`): brain accepts `questions` (always written,
default 5) and `tiebreak_questions`; ЭК `themes`; личная СИ `themes` and
`participants`; КСИ `themes`; ОД `tour_comp`. An unregistered key is a compile
error.

## `[init]` — seeding

`seed:` is one token: the reserved words `random` or `xlsx`, else a game slug
in this fest (unknown slug = compile error). Every source yields, per team,
either an exact rank or a basket; baskets resolve to ranks by the deterministic
Жребий lot. Dealing is always the snake: bands of G ranks, odd bands reversed
(the reference generate_kinsbf.py's PF_GROUPS pattern) — there is no separate
dealing key. The same snake deals reseed ranks into a block's groups.

- `seed: {game}` — that game's standings as metrics, ordered by `[init]`
  `sorting` (roster rating available as a metric).
- `seed: random` — every rank is a lot.
- `seed: xlsx` — an uploaded sheet carrying either an exact seeding column or a
  basket column.

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

Block keys:

| key | meaning |
|---|---|
| `type` | Kind: `roundrobin`, `single_elimination`, `double_elimination` (`swiss` parses, reports unimplemented) |
| `groups`, `teams_in_group` | roundrobin shape; each Group is one ranking scope. DE groups are always 4; `teams` (÷4) may stand in for `groups` |
| `teams` | elimination draw size (se: power of two), or how many a `flat` бой seats |
| `rounds` | play this many Rounds and stop: an rr Group's круги, or a bracket that ends short of a final (ТПШ's six winners) |
| `proceeding_teams` | block-grain Edge: how many advance per Group (rr) or overall |
| `reseed` | opt-in re-rank: `true` for the block's incoming Edge, a round code (`r4`) for a boundary inside an se block — that round then seats from the re-rank of the previous round, bracket-ordered — or `every` for both, the incoming Edge and every round after it (ТПШ) |
| `sorting`, `points` | ranking comparators and win/draw/loss values for this block's standings. A metric's direction follows the metric — место and жребий ascend, everything else descends — unless the scheme writes `taken asc`. On a block with `reseed: true` the sorting key describes the Edge instead (groups fall back to `[defaults]`/canon) and maps onto reseed metrics: `points`→place_sum asc, any metric the game's Protocol writes (`taken` for брейн, `total`/`plus`/`correct_50` for ЭК), plus the reseed's own `place_sum`, `draw` and the КИНСБФ 3.3.5 rates `points_share`, `taken_share`, `taken_base`, `diff` (desc; очки from final outcomes, взятые без перестрелок, разница against opponents in own bouts); default is place_sum, then taken. A group's `sorting` likewise names the Protocol's metrics or what a group adds (`points`, `h2h`, `taken`, `conceded`, `diff`, `place_sum`, `bouts`); anything else is a compile error naming the metric |
| `stats_from` | with a reseed only: which blocks' bouts the re-rank metrics are summed over (`stats_from: [s1, s2]`); default is the previous block, or the previous round for a boundary reseed. Naming the block itself at a boundary sums its own rounds so far (СтудЧР's ЭК ranked its пересев перед 1/4 by сумма мест over 1/16 and 1/8 together). Eligibility is independent of the stats scope: the previous block's proceeding places, or every place the previous round sent on |
| `best_of` | final Round only (`best_of.final: 3`, odd ≥ 3): the final becomes a series of identical бои («Финал. Бой k», one стол). No block can follow a series |
| `venues` | restrict the block (or, dotted, one Round) to a venue subset, by title or number |
| `bronze` | se only: add the 3rd-place match from the semifinal losers |
| `title` | display title: the stage title of a single-group block, a `{title}. Группа N` prefix on multi-group blocks |
| protocol keys | any registered param for the game's protocol |

Advancement between and inside blocks is deterministic by the Kind's canonical
templates (bracket order for eliminations, КИНСБФ cross templates between
blocks — taken from google_sheet_writer's generate_kinsbf.py conventions) so
teams change venues as little as possible. `reseed` replaces the template at
that one boundary with a global re-rank by `sorting`. The re-rank resolves like
a seed import: the host presses «Рассчитать» on the reseed panel (Протоколы
tab) once every source бой is finished; the ranks then seat the next block.

Blocks chain linearly; non-proceeding teams end with final classification from
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
deterministic and hyphenated (stage `s1-g2`, `s2-semifinal`, `s2-r16-w2`;
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
