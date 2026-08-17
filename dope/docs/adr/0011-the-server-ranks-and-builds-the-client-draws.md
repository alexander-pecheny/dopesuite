---
status: accepted
date: 2026-08-17
---

# The server ranks and builds a Game; the client draws it

Every standings table in dope is ranked once, on the server, by the Block's
Kind: `structure.Ranker.Standings` writes `stage_standings` (a distinct seat
order in `rank`, the shared display place and the sort metrics in
`metrics_json`) whenever the resolver runs, and ships each table's `sort`
rules with it. `domain/festview.Load` owns the fest view a page receives
(`StageView.Kind/Sort/Grain`, `Letter` on every бой); `domain/gamebuild`
(`Create`, `Recompile`, `Rebuild`) is the one path a Game's Structure is
materialised on, host page and importer alike. The client draws what it is
sent and ranks nothing.

This closed the 15 Aug 2026 architecture review's items #1, #2, #3 and #7
(commits `c98a5f4`, `cba2d5f`, `7e6370f`, `8ad113c`).

## Considered options

Client-side ranking was the incumbent: the ЭК page summed group tables from
match results with its own comparators, the brain page had a second copy, and
the reseed panel a third — three orderings that could disagree, and did. Moving
the sort rules to the server and keeping the summing on the client was tried
and rejected: the summing is where the metrics come from, and a metric the
client invents cannot be sorted by on the server or asserted by the replay.

## Consequences

- A page reads `stage.standings` and `stage.sort`; the columns of a standings
  table are the sort rules, in order. Nothing on the client re-derives a
  ranking; where a page still needs a split the server does not offer (ЭК's
  per-круг group table), it is a server feature to add, not a client sum.
- The Сетка's group table is a бой box — the same `article.grid-box` of
  `.grid-slot-cell`s as `buildMatchBox` emits — and every other
  standings-shaped table (пересев, статистика, площадки, составы, the brain
  crosstab) is `standingsTable(spec)` with `resultsTeamCell` as the one name
  cell. One skin by construction: a new table gets no CSS of its own.
- Rank sharing follows the sort keys (1, 2, 2, 4); a flat table sorted by its
  own keys shows that rank, one that keeps the бой's order shows the бой's
  place, mean of a tie and all (`shareRanks`, `flat.Standings`).
- Building goes through `gamebuild`; a host page or importer that writes
  `stages`/`matches` rows itself is a regression.
