# Полифест-2026, СИ, студенческий зачёт (регламент СИ, п. 3 и Приложение 1).
# 64 лучших онлайн-отбора, посев — лист с местами отбора. Четыре группы по 16,
# бои на четверых, пять кругов, каждый встречается с каждым один раз. За бой
# 5 − место + очки/200. Восемь из группы в Double Elimination: 1–4 места в
# верхнюю сетку, 5–8 в нижнюю, дальше бой за боем по Приложению 1.

[defaults]
venues: 4

[init]
seed: xlsx

[scheme]
title: Групповой этап
kind: roundrobin
slug: group-stage
groups: 4
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
group_size: 32
match_size: 4
winning_places: 2
lower_entrants: 16
opening: [E: A1 B2 C3 D4, F: B1 C2 D3 A4, G: C1 D2 A3 B4, H: D1 A2 B3 C4, I: A5 B6 C7 D8, J: B5 C6 D7 A8, K: C5 D6 A7 B8, L: D5 A6 B7 C8]
bouts.r2: [M: E-1 H-1 F-2 G-2, N: F-1 G-1 E-2 H-2, O: E-3 F-4 K-1 L-2, P: F-3 E-4 L-1 K-2, Q: G-3 H-4 I-1 J-2, R: H-3 G-4 J-1 I-2]
bouts.r3: [S: M-1 M-2 N-1 N-2, T: M-3 P-1 Q-1 O-2, U: N-3 O-1 R-1 P-2, V: M-4 N-4 R-2 Q-2]
bouts.r4: [W: S-3 T-1 V-2 U-2, X: S-4 U-1 V-1 T-2]
bouts.r5: [SF: W-1 X-1 W-2 X-2]
bouts.r6: [GF: S-1 S-2 SF-1 SF-2]
themes: 6
themes.r5: 8
themes.r6: 10
title.r1: 1 тур
title.r2: 2 тур
title.r3: 3 тур
title.r4: 4 тур
title.r5: 5 тур
title.r6: Гранд-финал
