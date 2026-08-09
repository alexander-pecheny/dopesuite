"""Reads СтудЧР's личная СИ workbook into the shape the transcript wants:
players in seed order, group membership, and every бой's marks, Σ and место.

The grid decoding — and the check that it is right — lives in sheetgrid.py,
shared with ТПШ."""
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import openpyxl
import sheetgrid

SRC = "sheets/sheet-1HOqiPxINFxW3NVu6QAOKuU8yOyXB3IwjGCrO6AqHtK4.xlsx"

wb = openpyxl.load_workbook(SRC, read_only=True, data_only=True)
players = []
for row in wb["Регистрация"].iter_rows(min_row=2, max_col=5, values_only=True):
    if row[0]:
        players.append({"name": str(row[0]).strip(), "seed": int(row[4]) if row[4] else None})

groups, header = {}, None
for row in wb["Группы"].iter_rows(max_col=24, values_only=True):
    cells = [str(c).strip() if c else "" for c in row]
    if any(c.startswith("Группа ") for c in cells):
        header = {i: c.split()[-1] for i, c in enumerate(cells) if c.startswith("Группа ")}
        continue
    if not header:
        continue
    for i, name in enumerate(cells):
        if i in header and name and not name.startswith("Пл."):
            groups.setdefault(header[i], []).append(name)

rounds = {}
for title in ["Круг 1 (протоколы)", "Круг 2 (протоколы)", "Круг 3 (протоколы)", "Круг 4 (протоколы)"]:
    rounds[title] = sheetgrid.read_bouts(wb[title])
playoff = sheetgrid.read_bouts(wb["Плей-офф (протоколы)"])
# The grand final sits on its own sheet because it is played over twelve themes
# where the rest of the play-off has eight, so its grid is a different width.
playoff += sheetgrid.read_bouts(wb["Грандфинал (протокол)"])
wb.close()

out = {"players": players, "groups": groups, "rounds": rounds, "playoff": playoff}
json.dump(out, open("si-data.json", "w"), ensure_ascii=False)
print("players", len(players), "groups", {g: len(v) for g, v in groups.items()})
for title, bouts in rounds.items():
    print(title, len(bouts), "боёв, первый:", bouts[0]["code"], [p["name"] for p in bouts[0]["players"]])
print("плей-офф боёв:", len(playoff))
print("тем, где сумма не сошлась:", len(sheetgrid.MISREADS))
for row in sheetgrid.MISREADS[:5]:
    print("   ", row)
