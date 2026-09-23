# Bug Major III, Тройка — one Game per зачёт, its entrants that зачёт's troikas.
# A written отбор of everybody, a Swiss stage of the best twelve (three wins go
# on, three losses go out), then the six: 1–6, 2–5, 3–4 in бои of two, and the
# three winners in a гранд-финал of nine темы at 1, 1, 1, 2, 2, 2, 3, 3, 3.

[scheme]
kind: flat
title: Отбор
written: true
themes: 9
theme_values: [1, 1, 1, 2, 2, 2, 3, 3, 3]
letters: false
sorting: [total, threes, twos, draw]
proceeding_participants: 12
---
kind: swiss
title: Швейцарка
participants: 12
wins: 3
losses: 3
---
kind: single_elimination
title: Плей-офф
participants: 6
themes: 8
match_size.r2: 3
themes.r2: 9
theme_values.r2: [1, 1, 1, 2, 2, 2, 3, 3, 3]
title.r1: Полуфиналы
title.r2: Гранд-финал
