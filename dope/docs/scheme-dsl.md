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
and are validated per game type against the registry in schemedsl
(`protocolParams`): brain registers `questions` and `tiebreak_questions`;
`themes` joins when the SI/EK protocols adopt the DSL. An unregistered key is
a compile error.

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

The DSL only *declares* the source. Resolution is the host pressing «Import
seed», which snapshots the source's current standings — partial results
mid-fest are a normal seed source — into seed rows (`imports/seed.go`
machinery: sourceRank, decline ticks, ladder, waitlist). Nothing recomputes
afterwards except tick/untick.

## `[scheme]` — blocks

Block keys:

| key | meaning |
|---|---|
| `type` | Kind: `roundrobin`, `single_elimination`, `double_elimination` (`swiss` parses, reports unimplemented) |
| `groups`, `teams_in_group` | roundrobin shape; each Group is one ranking scope. DE groups are always 4; `teams` (÷4) may stand in for `groups` |
| `teams` | elimination draw size (se: power of two) |
| `proceeding_teams` | block-grain Edge: how many advance per Group (rr) or overall |
| `reseed` | opt-in re-rank: `true` for the block's incoming Edge, or a round code (`r4`) for a boundary inside an se block — that round then seats from the re-rank of the previous round, bracket-ordered |
| `sorting`, `points` | ranking comparators and win/draw/loss values for this block's standings. On a reseed Edge, sorting maps onto reseed metrics (`points`→place_sum asc, `taken`→taken desc); default is place_sum, then taken |
| `venues` | restrict the block (or, dotted, one Round) to a venue subset, by title or number |
| `bronze` | se only: add the 3rd-place match from the semifinal losers |
| `title` | display title for a single-group block |
| protocol keys | any registered param for the game's protocol |

Advancement between and inside blocks is deterministic by the Kind's canonical
templates (bracket order for eliminations, КИНСБФ cross templates between
blocks — taken from google_sheet_writer's generate_kinsbf.py conventions) so
teams change venues as little as possible. `reseed` replaces the template at
that one boundary with a global re-rank by `sorting`.

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
surviving match's identity (state, journal, SSE scopes). The save flow shows a
create/delete diff and requires explicit confirmation to delete a match with
entered results.

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
