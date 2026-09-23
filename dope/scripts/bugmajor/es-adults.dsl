# Bug Major III, Эрудит-секстет, взрослый зачёт: the sixteen best adult
# teams after three tours of ОД. Put the ОД Game's code after `seed:`.
# Two group games four to a table, dealt by the snake, the halls rotating for
# the second; the stage score is Σ + 200 − 50 × место in each game. The best
# eight play two semifinals (1-4-5-8, 2-3-6-7), two of each go to the final.

[init]
seed: od
division: -Студ

[scheme]
kind: placement
title: Групповой этап
participants: 16
match_size: 4
deal: snake
rotation: true
title.r1: Игра №1
title.r2: Игра №2
bout.stage: total + 200 - 50 * place
sorting: [stage, plus, correct_50, draw]
proceeding_participants: 8
---
kind: single_elimination
title: Плей-офф
participants: 8
match_size: 4
winning_places: 2
