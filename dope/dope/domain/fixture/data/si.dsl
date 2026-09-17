[defaults]
venues: 4

[scheme]
title: Групповой этап
kind: roundrobin
slug: group-stage
group_size: 9
match_size: 3
themes: 6
bout.points: seats + 1 - place
sorting: [points, total, plus]
proceeding_participants: 4
---
title: Финал
kind: single_elimination
participants: 4
match_size: 4
winning_places: 1
themes: 8
reseed: true
sorting: [place_sum, total, plus, taken50, taken40, taken30, taken20]
