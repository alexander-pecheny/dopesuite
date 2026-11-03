# Полифест-2026, КИнСбФ, студенческий зачёт (регламент КИнСбФ, Приложение 1).
# До 32 лучших студенческих команд по итогам первого дня ОД. Восемь групп по
# четыре, порядок боёв из Приложения, 3/1/0 очка. Двое из группы в Double
# Elimination: победители групп в верхнюю сетку, вторые места в нижнюю.
# Бой из 5 вопросов, финал из 9.

[defaults]
venues: 8
questions: 5
points: [3, 1, 0]
sorting: [points, diff, taken]

[init]
seed: od-1   # the ОД game's code: create the ОД first
division: Студ
sorting: [points desc, rating desc]

[scheme]
title: Групповой этап
kind: roundrobin
groups: 8
group_size: 4
pairings: [1-3 2-4, 1-2 3-4, 4-1 2-3]
proceeding_participants: 2
---
title: Плей-офф
kind: double_elimination
groups: 1
group_size: 16
lower_entrants: 8
opening: [N: A1 B1, O: C1 D1, P: E1 F1, Q: G1 H1, R: A2 B2, S: C2 D2, T: E2 F2, U: G2 H2]
bouts.r2: [V: N-1 O-1, W: P-1 Q-1, X: N-2 S-1, Y: O-2 R-1, Z: P-2 U-1, AA: Q-2 T-1]
bouts.r3: [AB: V-1 W-1, AC: V-2 AA-1, AD: W-2 Z-1, AE: X-1 Y-1]
bouts.r4: [AF: AB-2 AE-1, AG: AC-1 AD-1]
bouts.r5: [AH: AF-1 AG-1]
bouts.r6: [Финал: AB-1 AH-1]
questions.r6: 9
title.r1: 1 тур
title.r2: 2 тур
title.r3: 3 тур
title.r4: 4 тур
title.r5: 5 тур
title.r6: Финал
