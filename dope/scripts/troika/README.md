# Троечка VIII Octobearfest on dope

One reader, one scheme, one transcript — the seam is the same
[replay transcript](../../docs/replay-transcript.md) СтудЧР uses (ADR-0010).

| reads | emits |
|---|---|
| `read-troika-sheets.py` | `testdata/octobearfest2025/troika.transcript` |

```sh
uv run --with openpyxl python scripts/troika/read-troika-sheets.py troika.xlsx
```

The workbook is not in the repo — it is the tournament's own Google Sheet
(`docs.google.com/spreadsheets/d/1kJHMMkjYlQ6yRT8y85W_wJkv5pOdK_xeERU-XUe5pGk`),
exported as `.xlsx`. The transcript it produces is committed, and that is what
the replay reads.

## What the sheet can and cannot check

The протоколы count, per вопрос, **how many of the three answered it** — never
which кресло. So:

- Σ, место, and every группа's table are the sheet's own numbers, asserted бой
  by бой and table by table. The рейтинговый балл especially: the sheet computed
  «1 / 0.5 / 0 плюс очки, делённые на 50» in its own formulas, and the scheme
  says the same thing as `standings.rating: points + taken / 50`.
- The кресла are synthesized on replay — the first N of the three are marked
  верно (see `playTroika`). Total faithful, composition invented, exactly as a
  перестрелка's marks are.
- **Статистика has no oracle here.** It is the one tab that reads кресла, and
  the sheet does not record them.

## Two things the transcript records as given rather than asserted

- **Посев.** The оргкомитет seeded from two other disciplines of the фест
  (регламент 4.4.2), which the workbook holds on its own «Посев» tab. The reader
  numbers the roster so that the scheme's snake deal lands each team in the
  группа the sheet put it in; the посев itself is input.
- **The order of бои inside a группа.** dope's round-robin rotates one way and
  the tournament's rotated the other — the same fifteen pairs, grouped into
  круги differently — so every группа бой is written `жребий`. Which pairs are
  played is dope's; which круг each falls in is the tournament's.
