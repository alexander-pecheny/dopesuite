# Полифест-2026, СИ, молодёжный зачёт (регламент СИ, п. 3 и Приложение 2).
# 32 лучших онлайн-отбора, посев — лист с местами отбора. Две группы по 16,
# бои на четверых, пять кругов. За бой 5 − место + очки/200. Восемь из группы
# в Double Elimination по Приложению 2; в 3-м туре нижней сетки бои на троих.

[defaults]
venues: 2

[init]
seed: xlsx

[scheme]
title: Групповой этап
kind: roundrobin
slug: group-stage
groups: 2
group_size: 16
match_size: 4
themes: 5
bout.points: 5 - place + total / 200
sorting: [points, plus]
proceeding_participants: 8
---
title: Плей-офф
kind: double_elimination
groups: 1
group_size: 16
match_size: 4
winning_places: 2
lower_entrants: 8
opening: [E: A1 B2 B3 A4, F: B1 A2 A3 B4, G: A5 B6 B7 A8, H: B5 A6 A7 B8]
bouts.r2: [I: E-1 F-1 E-2 F-2, J: E-3 F-4 H-1 G-2, K: F-3 E-4 G-1 H-2]
bouts.r3: [L: I-3 J-1 K-2, M: I-4 K-1 J-2]
bouts.r4: [SF: L-1 M-1 L-2 M-2]
bouts.r5: [GF: I-1 I-2 SF-1 SF-2]
themes: 6
themes.r4: 8
themes.r5: 10
title.r1: 1 тур
title.r2: 2 тур
title.r3: 3 тур
title.r4: 4 тур
title.r5: Гранд-финал
