# СтудЧР-2026 on dope

The championship is carried across in two steps, and the seam between them is a
[replay transcript](../../docs/replay-transcript.md): a `read-*` script knows one
workbook and nothing about dope, an `emit-*` script writes the transcript, and
the replayer plays it through dope's real handlers and holds the result against
what the sheets claimed. Another tournament's flavour of Google Sheets needs a
new reader and nothing else.

| game | reads | emits |
|---|---|---|
| ЭК | `read-ek-sheet.py` | `emit-ek.py` → `testdata/studchr2026/ek.transcript` |
| личная СИ | `read-si-sheets.py` | `emit-si.py` → `testdata/studchr2026/si.transcript` |
| ТПШ | `read-tpsh-sheet.py` | `emit-tpsh.py` → `testdata/studchr2026/tpsh.transcript` |
| брейн | `read-brain-sheets.py` | `emit-brain.py` → `testdata/studchr2026/brain.transcript` |
| ОД | `read-od-sheet.py` | — |

СИ and ТПШ print the same grid, so its decoding lives once in `sheetgrid.py`.

Each reader checks its own decoding rather than trusting it. СИ's recomputes
every theme's total from the marks it decoded and compares it with the total the
sheet itself printed; ОД's checks that all 1853 takings name a team on the list.

The schemes live beside them as `ek.dsl`, `si.dsl` and `tpsh.dsl`, and the same text is in
`schemedsl/studchr_test.go`, where it is compiled against the регламент.

## What the replays prove

ЭК: 48 teams, 25 бои. Every Σ is derived from the hosts' marks and 86 of the 96
places with them; ten places are pinned.

Личная СИ: 54 players, 96 бои — six групп of nine playing four круги three at a
table, then a play-off of 24 on two lives with a пересев before every round. Not
one бой is a жребий. The roster order is the whole input: the snake deals the
групп from it and every play-off бой is seated from the round before, so the
replay asserts the entire seating of the tournament.

Eight places are pinned, in four бои where two players finished level on Σ and
the sheet still ranked them apart.

ТПШ: 91 players, 10 бои. One written отбор seating everyone, then a bracket of 24
that stops after its second stage — the six left are the winners and there is no
final. Both bracket rounds are seeded from a пересев, so the отбор's ranking is
checked where it decides something: in whom бой A seats.

Брейн: 48 teams, 132 бои over five blocks — twelve групп of four, six DE pods, a
пересев, a group stage of four threes, another of two fours, and semifinals with
a best-of-three final. Only the жеребьёвка is input; every pod, every later group
and every finalist is seated from what came before, so the replay asserts the
entire structure of the largest game dope runs.

## For the tournament's author

One thing to rule on. The брейн пересев (регламент 3.3.5) ranks by % очков, then
разница, then % взятых — but the «Пересев» tab sorted % взятых before разница,
and that ordering is what seated the 2-й групповой этап as it was actually
played. `brain.dsl` reproduces the tournament and therefore the sheet;
`kinsbfSrc` in `schemedsl/compile_test.go` keeps the регламент, and the two
disagree on purpose. If the регламент is what should have happened, the fest is
faithful to a mistake.

## ОД has its own фест

At СтудЧР ОД had 65 teams where the брейн had 48, and «команда 12» means a
different team in each. A flat game takes its teams from the фест's roster, and
`team_list_source` is declared but never read, so ОД lives in its own фест. That
is how the tournament looked anyway, and it needs no new feature — but a фест per
game is not what a фест is for, and the seam should be closed.

ОД's own transfer is checked against the sheet's «Итог» tab: all 65 totals agree
to the unit. R differs by a couple of percent, which is the бухгольц denominator
and not the data.

## Not carried across yet

Составы — who played for which team, and which player took which question — need
a team roster the фест does not have. They do not affect Σ or место.

КСИ was never played. Of the six workbooks the оргкомитет handed over none is a
командная своя игра — the one that looked like it is ТПШ, which is now carried
across — so the КСИ game does not belong on the фест at all.
