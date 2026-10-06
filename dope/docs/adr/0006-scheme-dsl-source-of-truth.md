---
status: accepted
date: 2026-08-02
---

# The scheme DSL is the source of truth; detailed schemes are its compiled IR

Detailed scheme JSON (ADR-0001's authoring document) turned out to be a machine format: chr2026's EK bracket is ~1500 lines of hand-wired matches and slot refs, and every new tournament would need a bespoke generator script. We decided a game's structure is authored in a small declarative DSL (grammar borrowed from xy's `.hndt`: `[section]` headers, `key: value` lines, `---` block separators) stored as `games.scheme_dsl`, and the detailed JSON becomes derived IR — recompiled on every DSL edit, targeting the existing model rows (`rr`/`se`/`matches`/`reseed` stages) so the resolver is untouched. The DSL speaks the Block/Edge vocabulary (CONTEXT.md): a `[scheme]` block is one Kind invocation that macroexpands into Rounds of Matches; advancement rules live on Edges. Hand-authoring detailed JSON remains only as an escape hatch that detaches the game from its DSL, and is the sole route to the `manual` Kind. We rejected keeping the JSON canonical with the DSL as a one-shot generator (a point-and-click builder would then have to reverse-engineer structure from the expansion) and migrating the runtime model to Blocks first (blocks the DSL on a risky rewrite of a working resolver).

## Consequences

- Compilation is deterministic with stable codes (`s1-g2`, `s2-semifinal-m1`), so recompiling an edited DSL preserves surviving matches' identity — state, journal, SSE scopes. A recompile only changes what has not started: a бой with entered marks must survive with identical slot sources or the edit is refused naming it.
- Config cascades defaults < block < round: `[defaults]` holds cross-cutting and protocol params (`themes: 6`), blocks redeclare differences, dotted keys (`themes.final: 12`, `reseed: r4`, `venues.final`) address a Kind's canonical round codes.
- `[init]` declares seeding (`seed: {game}|random|xlsx`; exact ranks or baskets, deterministic lots where unranked) but resolves nothing: the host's «Import seed» snapshots the source's *current* standings (partial ОД mid-fest is normal), and only decline ticks move the ladder afterwards — `imports/seed.go` generalizes off its ksi→ek hardcode.
- Advancement is deterministic by each Kind's canonical template (teams keep venues); `reseed:` is opt-in per Round boundary. v1 chains blocks linearly — eliminated teams get final classification; consolation brackets are a later Edge extension.
- v1 DSL kinds: `roundrobin`, `single_elimination`, `double_elimination`; `swiss` parses but reports unimplemented; `manual` is not a DSL word.
- The game creation form's per-type fields become template prefill for the DSL textarea; the future visual builder edits the same column.

## Amended 2026-10-06 (every bracket Game has a scheme)

Pasted detailed JSON is no longer accepted. The creation form's JSON field,
its twin's `scheme` field, the fest-wide scheme import (`/api/import`,
`/api/fest/{fest}/scheme-import` and the host page `/host/fest/{fest}/import`)
and `gamebuild.Materialise` are gone. A Game with a bracket is created from a
DSL or not at all. The flat formats (ОД, КСИ, Мультиигры, Кубок Дружбы) are
not affected: they never had a DSL and are shaped by the creation form's
fields.

The escape hatch had one real use, chr2026's ЭК bracket (production's
chr2026/ek-3, a finished July fest). Games like it keep their stored scheme
and no DSL. They still render, score, take a seed import and clear (a clear
rebuilds them from the stored scheme). They are never recompiled:
`festops.UpdateSettingsTx` and `gamebuild.Recompile` refuse a scheme edit with
a message from the Catalog. Before this, the settings page hid the editor for
such a Game but the JSON twin recompiled it from any text, which would have
replaced the bracket. The `manual` Kind stays in the code only for these
legacy Games.

A scheme that does not compile now reaches the JSON twin as a User Error
(400) with the compiler's text, as the form already showed it.
