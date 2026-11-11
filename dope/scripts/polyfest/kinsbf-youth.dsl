# Полифест-2026, КИнСбФ, молодёжно-фестивальный зачёт (регламент КИнСбФ,
# Приложение 1). До 16 лучших нестуденческих команд по итогам первого дня ОД.
# Четыре группы по четыре, порядок боёв из Приложения, 3/1/0 очка. Двое из
# группы в Double Elimination; проигравший финал верхней сетки ждёт тур и
# играет с победителем боя L. Бой из 5 вопросов, финал из 9; ничья в
# плей-офф решается перестрелкой.

[defaults]
venues: 4
questions: 5
points: [3, 1, 0]
sorting: [points, diff, taken]

[init]
seed: od-1[tours 1..3]   # ОД's first game day; od-1 is the ОД game's code, so create the ОД first
division: -Студ

[scheme]
title: Групповой этап
kind: roundrobin
groups: 4
group_size: 4
pairings: [1-3 2-4, 1-2 3-4, 4-1 2-3]
proceeding_participants: 2
---
title: Плей-офф
tiebreak_questions: true
kind: double_elimination
groups: 1
group_size: 8
lower_entrants: 4
opening: [E: A1 B1, F: C1 D1, G: A2 B2, H: C2 D2]
bouts.r2: [I: E-1 F-1, J: E-2 H-1, K: F-2 G-1]
bouts.r3: [L: J-1 K-1]
bouts.r4: [M: I-2 L-1]
bouts.r5: [Финал: I-1 M-1]
questions.r5: 9
title.r1: 1 тур
title.r2: 2 тур
title.r3: 3 тур
title.r4: 4 тур
title.r5: Финал
