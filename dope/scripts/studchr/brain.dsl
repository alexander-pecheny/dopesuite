[defaults]
venues: 6
questions: 5

[scheme]
title: 1-й групповой этап
kind: roundrobin
groups: 12
group_size: 4
proceeding_participants: 2
---
kind: double_elimination
groups: 6
proceeding_participants: 2
---
title: 2-й групповой этап
kind: roundrobin
groups: 4
group_size: 3
reseed: true
stats_from: [s1, s2]
# WRONG ON PURPOSE. Регламент 3.3.5 ranks by % очков, разница, % взятых; the
# «Пересев» tab sorted % взятых before разница, and that is what seated the
# 2-й групповой этап. Kept so the transfer reproduces the tournament as played.
# A future чемпионат uses the регламент order — it is in kinsbfSrc already.
sorting: [points_share desc, taken_share desc, diff desc]
proceeding_participants: 2
---
title: 3-й групповой этап
kind: roundrobin
groups: 2
group_size: 4
proceeding_participants: 2
---
title: Финальный этап
kind: single_elimination
participants: 4
bronze: true
questions: 7
questions.final: 5
best_of.final: 3
