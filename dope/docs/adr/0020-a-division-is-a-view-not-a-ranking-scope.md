---
status: accepted
date: 2026-09-15
---

# A Division is a view, not a ranking scope

A tournament may run several зачёты on the same questions — a student
championship where adult teams play вне зачёта, an open championship with a
national medal table inside it. The rating site marks a team's зачёт as a
Flag on the team; ОД and КСИ had no idea of it and ranked everyone in one
table.

The obvious home for a зачёт is the server: a partition key on the flat
Block's standings, so that each Division is a Ranking scope of its own and an
Edge could one day seat «лучшая студенческая команда» into a final. We did not
do that.

## Decision

A Division is a filter the viewer chooses — `?division=<short name>` on the
ОД/КСИ results and detailed tabs and the ОД Экран — and the page re-ranks the
Protocol document in the browser within the chosen set, which is where those
tabs already rank it (ADR-0018). Places are dealt afresh inside the Division;
per-question metrics that depend on the whole field (ОД's рейтинг) are still
reckoned over everyone, since everyone played the question. The server stores
the Flags on the fest team and propagates them into the game state with the
roster; it never ranks a Division and `stage_standings` knows nothing of them.

## Why

Every Division we have met is a way of *looking at* one game's table, not a
thing the Structure advances anyone by: team games that need separate
standings play separate Games. Ranking Divisions server-side would add a
partition to `flat.Standings`, a second standings row per team, and a URL
that must match a stored key — for a report nobody feeds into an Edge. A
viewer-side filter keys on the Flag's short name, works for hand-typed Flags
without a rating id, and is one function over rows the page already has.

If a регламент ever advances a team by its place within a Division, that is
the day a Division becomes a Ranking scope: add the partition to the flat
Block's standings then, and keep this filter as the way to look at it.
