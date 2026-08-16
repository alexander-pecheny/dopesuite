---
status: accepted
date: 2026-08-08
---

# Group scoring is written as expressions, not chosen from a registry

Every format scores its groups differently — КИнСБФ pays 2/1/0, СИ pays «4 − место» plus a score tiebreak, ЭК and СИ rank on взятые за 50/40/30/20, and the пересев ranks on rates that were hardcoded in Go. We decided a scheme author writes these as arithmetic rather than picking a registered rule name, at two grains: `bout.<name>:` is evaluated per Participant per Match over that бой's outcome and summed into the standings, and `standings.<name>:` is evaluated once over those sums. `sorting:` then names any Metric, raw or derived. The alternative — registering named scoring rules in Go alongside Kinds and Protocols — is more consistent with the architecture but makes every new tournament a code change, which is precisely the cost we were trying to remove.

## Consequences

- The two grains are not interchangeable and both are needed: summing `4 − место` equals `4 × бои − сумма мест`, but 3/1/0 cannot be recovered from сумма мест, and `очки / (2 × бои)` cannot be computed before the sum.
- The grammar is arithmetic, comparisons yielding 1/0, and `cond ? a : b` — enough for count-of-wins tiebreaks and non-midpoint draws. The DSL splits a line on its first colon, so a ternary parses. It can grow later; it should not grow speculatively.
- A Match expression sees its own seat's Metrics, the бой facts `seats` and `questions`, opponents in slot order (`opp1`…) and their sums (`opp_taken`). Личная встреча stays a named comparator, not an expression, because it is a second pass over whichever Participants are still tied.
- Metric names are validated: each Protocol declares what it emits, the Structure declares what it derives, and a typo is a compile error rather than a silently wrong ranking discovered mid-tournament.
- `points:` survives as sugar for the common case — a place→очки table, one entry per place, unlisted places interpolated so a shared place pays the mean. КИнСБФ's 2/1/0 is `[2, 0]`.
- The пересев Edge loses its hardcoded `points_share`/`taken_share`/`diff` and uses the same machinery.
