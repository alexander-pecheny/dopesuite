# Хамса, фестиваль «Город героев-2026» (Волгоград, 3 октября 2026).
# Регламент: письменный отбор играется отдельной игрой КСИ, отсюда её
# двенадцать лучших садятся в групповой этап; ksi-1 — код той игры в фесте.

[defaults]
venues: [А, Б, В]

[init]
seed: ksi-1

[scheme]
title: Групповой этап
kind: placement
participants: 12
match_size: 4
rounds: 2
title.r1: Игра №1
title.r2: Игра №2
proceeding_participants: 4
sorting: [place_sum, total, first, seed]
---
title: Финал
kind: flat
participants: 4
reseed: true
stats_from: [s1]
sorting: [place_sum, total, first, seed]
shootout: true
