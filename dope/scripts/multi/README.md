# «Ассорти» on dope

One reader, one fixture. Мультиигры is a flat game — one sitting, no бои — so
there is nothing to replay бой by бой the way Троечка is replayed; what the
workbook is good for is its arithmetic, and that is what the fixture holds
(`domain/games/assorti_test.go`).

| reads | emits |
|---|---|
| `read-assorti-sheets.py` | `testdata/assorti2025/assorti.json` |

```sh
uv run --with openpyxl python scripts/multi/read-assorti-sheets.py assorti.xlsx
```

The workbook is not in the repo — it is the fest's own Google Sheet, exported
as `.xlsx`. The fixture it produces is committed, and that is what the test
reads.

## What it holds dope to

Two мини-игры on 68 teams, each normalised to a hundred and then added:

- «Медиа-эрудит» — eight темы of ten вопросы at 10, 10, 20, 20 … 50, 50, a
  cell being верно, неверно or empty. Best Σ 980.
- «Не только песни» — 72 columns worth a балл each. Best Σ 55.

The two differ in scale by a factor of twenty, which is the whole reason the
format normalises. Every number the sheet printed is asserted: each team's Σ in
each мини-игра, each «сколько от лучшего» out of a hundred, and the Итог.

Two things the sheet does that dope does not, and the test says so rather than
papering over them:

- **Вне зачёта.** Six teams played but were not ranked; they keep a Σ on the
  sheet and take no place. dope has that already — it is «Отказы» — and such a
  team also does not set the scale for everyone else, which is what the sheet's
  divisors show (980 among the ranked, not the 1570 somebody outside scored).
- **Ties.** The sheet numbers 1…62 straight through, so two teams level on the
  Итог still get different numbers. dope shares a place. The test holds the
  order rather than the number: nobody the sheet put ahead may end up behind.
