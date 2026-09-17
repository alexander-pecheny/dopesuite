[defaults]
points: [1, 0.5, 0]
sorting: [rating, h2h, taken, diff]

[scheme]
title: Групповой этап
kind: roundrobin
groups: 4
group_size: 4
match_size: 2
themes: 6
metric: total
standings.rating: points + taken / 50
proceeding_participants: 1
---
title: Финальный этап
kind: single_elimination
participants: 4
bronze: true
reseed: true
themes: 6
metric: total
standings.rating: points + taken / 20
sorting: [rating]
