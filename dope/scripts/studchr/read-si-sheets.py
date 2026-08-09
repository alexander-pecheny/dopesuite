"""Reads СтудЧР's личная СИ workbook into the shape dope's importer wants:
players in seed order, group membership, and every бой's per-question marks.

The sheet marks a taken question «й» and a missed one «ц»; a blank is an
unanswered question. Themes repeat every 7 columns from column 4."""
import json, openpyxl, sys

SRC = "sheets/sheet-1HOqiPxINFxW3NVu6QAOKuU8yOyXB3IwjGCrO6AqHtK4.xlsx"
# The sheets were filled by hand over two days, so a taken question is written
# «й» or «q» (the same key on either layout) or a positive number, and a missed
# one «ц»/«w» or a negative. A zero or a blank is a question nobody took. Every
# decoded theme is checked against the sheet's own theme total below.
RIGHT = {"й", "q", "y", "+"}
WRONG = {"ц", "w", "-"}
THEME_STRIDE, FIRST_VALUE_COL, VALUES = 7, 4, 5


def mark(cell):
    if cell is None:
        return ""
    text = str(cell).strip().lower()
    if text in RIGHT:
        return "right"
    if text in WRONG:
        return "wrong"
    try:
        value = float(text)
    except ValueError:
        return ""
    if value > 0:
        return "right"
    if value < 0:
        return "wrong"
    return ""


MISREADS = []


def theme_count(header):
    """A бой has as many themes as its header names — six in the group stage,
    eight in the play-off, twelve in the grand final. Everything to the right of
    them is the statistics block, not marks."""
    count = 0
    for t in range(20):
        base = FIRST_VALUE_COL + t * THEME_STRIDE
        if base + VALUES >= len(header):
            break
        label = header[base + VALUES]
        if isinstance(label, str) and label.strip().startswith("Тема"):
            count += 1
    return count


def number(cell):
    if isinstance(cell, (int, float)):
        return float(cell)
    try:
        return float(str(cell).strip().replace(",", "."))
    except (ValueError, AttributeError):
        return None


def read_bouts(ws):
    """Each block is «Бой X» plus its player rows, until a blank line."""
    bouts, current, themes_here = [], None, 0
    for row in ws.iter_rows(values_only=True):
        head = str(row[0]).strip() if row[0] else ""
        if head.startswith("Бой "):
            current = {"code": head.split("(")[0].strip(), "players": []}
            themes_here = theme_count(row)
            bouts.append(current)
            continue
        if not head or current is None:
            current = None
            continue
        themes = []
        for t in range(themes_here):
            base = FIRST_VALUE_COL + t * THEME_STRIDE
            if base + VALUES > len(row):
                break
            answers = [mark(row[base + i]) for i in range(VALUES)]
            themes.append(answers)
            # The sheet computed each theme's total; recomputing it from the
            # decoded marks is the check that the decoding is right.
            stated = row[base + VALUES]
            if isinstance(stated, (int, float)):
                got = sum((10 * (i + 1)) * (1 if a == "right" else -1 if a == "wrong" else 0)
                          for i, a in enumerate(answers))
                if got != round(stated):
                    MISREADS.append((head, t + 1, got, round(stated), [str(row[base + i]) for i in range(VALUES)]))
        # Σ and место as the sheet printed them. They are what the replay holds
        # dope against, so they are read verbatim and never recomputed here.
        current["players"].append({"name": head, "themes": themes,
                                   "total": number(row[1]), "place": number(row[2])})
    return [b for b in bouts if b["players"]]


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
    rounds[title] = read_bouts(wb[title])
playoff = read_bouts(wb["Плей-офф (протоколы)"])
# The grand final sits on its own sheet because it is played over twelve themes
# where the rest of the play-off has eight, so its grid is a different width.
playoff += read_bouts(wb["Грандфинал (протокол)"])
wb.close()

out = {"players": players, "groups": groups, "rounds": rounds, "playoff": playoff}
json.dump(out, open("si-data.json", "w"), ensure_ascii=False)
print("players", len(players), "groups", {g: len(v) for g, v in groups.items()})
for title, bouts in rounds.items():
    print(title, len(bouts), "боёв, первый:", bouts[0]["code"], [p["name"] for p in bouts[0]["players"]])
print("плей-офф боёв:", len(playoff))
print("тем, где сумма не сошлась:", len(MISREADS))
for row in MISREADS[:5]:
    print("   ", row)
