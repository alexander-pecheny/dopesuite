# Троечка VIII Octobearfest — регламент 4.5–4.7, 5.1–5.5.
#
# Рейтинговый балл — 1 за победу, 0.5 за ничью, 0 за поражение, плюс игровые
# очки, делённые на пятьдесят (в финале — на двадцать). Он и решает: в группе
# первым, дальше личная встреча, забитые, разница.
[defaults]
points: [1, 0.5, 0]
sorting: [rating, h2h, taken, diff]

[scheme]
title: 1-й групповой этап
kind: roundrobin
groups: 8
group_size: 6
match_size: 2
themes: 6
metric: total
standings.rating: points + taken / 50
proceeding_participants: 2
---
title: 2-й групповой этап
kind: roundrobin
groups: 4
group_size: 4
match_size: 2
themes: 6
metric: total
standings.rating: points + taken / 50
proceeding_participants: 2
---
title: 3-й групповой этап
kind: roundrobin
groups: 2
group_size: 4
match_size: 2
themes: 8
metric: total
standings.rating: points + taken / 50
proceeding_participants: 2
---
title: Финальный этап
kind: single_elimination
participants: 2
bronze: true
themes: 6
metric: total
best_of.final: 3
best_of.bronze: 3
standings.rating: points + taken / 20
sorting: [rating]
