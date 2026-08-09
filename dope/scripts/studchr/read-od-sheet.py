"""Reads СтудЧР's ОД (КВРМ) book into dope's own ОД state.

The «Ввод» sheet already IS dope's state, transposed: one column per question,
holding the numbers of the teams that took it, plus a row saying which questions
have been entered. «Подробно» carries the team list with its numbers."""
import json, openpyxl

SRC = "sheets/sheet-1RbdnnqT1NyAvPJeg8FcN4Lej1bkH4nveBpr-Ac5jeVI.xlsx"

wb = openpyxl.load_workbook(SRC, read_only=True, data_only=True)
teams = []
# № is the tournament number the «Ввод» grid keys on; ID is the team's
# rating.chgk.info id and means nothing here.
for row in wb["Подробно"].iter_rows(min_row=2, max_col=4, values_only=True):
    if row[0] and row[2]:
        teams.append({"number": int(row[0]), "name": str(row[2]).strip(),
                      "city": str(row[3]).strip() if row[3] else ""})

grid = list(wb["Ввод"].iter_rows(values_only=True))
wb.close()
# The question columns are not contiguous — тур separators sit between them —
# so each question's column is taken from the header rather than counted off.
columns = [i for i, c in enumerate(grid[0]) if isinstance(c, (int, float))]
questions = len(columns)
completed = [bool(grid[1][col]) for col in columns]
entries = []
for col in columns:
    takers = []
    for row in grid[2:]:
        if col < len(row) and isinstance(row[col], (int, float)):
            takers.append(int(row[col]))
    entries.append(sorted(takers))

known = {t["number"] for t in teams}
strays = sorted({n for takers in entries for n in takers} - known)
print(f"команд {len(teams)}, вопросов {questions}, введено {sum(completed)}")
print("взятий:", sum(len(e) for e in entries), "| номеров вне списка:", strays[:5] or "нет")
json.dump({"teams": teams, "entries": entries, "completed": completed},
          open("od-data.json", "w"), ensure_ascii=False)
