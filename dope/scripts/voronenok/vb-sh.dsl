# Интеллектуальный воронёнок 2026, «Вопросики быстрые», группа Ш.
# Отборочный этап по швейцарской системе (регламент 3.1), плей-офф по
# приложению: верхняя сетка — Квал1–4, нижняя — Квал5–8, финал до двух побед.

[defaults]
questions: 5

[scheme]
kind: swiss_classic
title: Отборочный этап
rounds: 7
zero_draw_points: 0
proceeding_participants: 8
---
kind: double_elimination
title: Плей-офф
groups: 1
participants: 8
lower_entrants: 4
tiebreak_questions: true
opening: [I.1: A1 A4, I.2: A2 A3, I.3: A5 A8, I.4: A6 A7]
bouts.r2: [II.1: I.3-1 I.1-2, II.2: I.4-1 I.2-2]
bouts.r3: [III.1: I.1-1 I.2-1, III.2: II.1-1 II.2-1]
bouts.r4: [IV.1: III.2-1 III.1-2]
questions.r3: 7
questions.r4: 7
proceeding_participants: 2
---
kind: single_elimination
title: Финал
participants: 2
best_of.final: 3
tiebreak_questions: true
