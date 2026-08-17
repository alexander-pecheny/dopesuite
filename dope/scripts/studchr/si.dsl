[defaults]
venues: 6

[scheme]
title: Групповой этап
kind: roundrobin
slug: group-stage
groups: 6
group_size: 9
match_size: 3
themes: 6
bout.points: seats + 1 - place
sorting: [points, total, plus]
proceeding_participants: 4
---
title: Плей-офф
kind: double_elimination
participants: 24
match_size: 4
winning_places: 2
themes: 8
themes.r7: 12
reseed: true
sorting: [place_sum, total, plus, taken50, taken40, taken30, taken20]
title.r1: 1 этап
title.r2: 2 этап
title.r3: 3 этап
title.r4: 4 этап
title.r5: 5 этап
title.r6: Финал нижней сетки
title.r7: Грандфинал
