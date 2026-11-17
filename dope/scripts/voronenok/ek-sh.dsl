# Интеллектуальный воронёнок 2026, Эрудит-квартет, группа Ш (регламент 3.2,
# приложение 2). 32 команды по посеву из Вопросиков; в бою четыре команды,
# дальше проходят двое. Бои 1/8 — змейка посева (A: 1 16 17 32), дальше бои
# расписаны как в приложении. Бои 1/8 получают буквы A–H, их места пишутся
# номером боя: «1-1» — первое место боя A, «8-1» — боя H.

# The seed (1.5) names the Вопросики ОД by its code once the fest has it:
# [init]
# seed: <код ОД>[tours 1..2]
# sorting: [total, rating, tour2]
# before_rulings: true
# division: Ш

[scheme]
kind: single_elimination
title: Эрудит-квартет
participants: 32
match_size: 4
winning_places: 2
title.r1: 1/8 финала
title.r2: 1/4 финала
title.r3: 1/2 финала
title.r4: Финал
bouts.r2: [I: 1-1 4-2 5-2 8-1, J: 2-1 3-2 6-2 7-1, K: 3-1 2-2 7-2 6-1, L: 4-1 1-2 8-2 5-1]
bouts.r3: [M: I-1 J-2 K-2 L-1, N: J-1 I-2 L-2 K-1]
bouts.r4: [Финал: M-1 M-2 N-1 N-2]
