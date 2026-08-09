"""Reads СтудЧР's ЭК book into per-бой marks.

Each team takes two rows: the first carries its name, Σ, место and the player
who played each theme; the second carries that theme's five marks. Themes
repeat every 7 columns from column 4, and each бой's theme count is however
many «Т…» headers its block names."""
import json, openpyxl

SRC = "sheets/sheet-1kwtZUpGtFxkJYMIHeRP40ApKfhpl-75RJI_N0O3eqMo.xlsx"
ROUNDS = ["116", "18", "14", "12", "Финал"]
RIGHT, WRONG = {"й", "q", "y", "+"}, {"ц", "w", "-"}
STRIDE, FIRST, VALUES = 7, 4, 5


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
    return "right" if value > 0 else "wrong" if value < 0 else ""


def theme_count(header):
    count = 0
    for t in range(20):
        base = FIRST + t * STRIDE
        if base + VALUES >= len(header):
            break
        label = header[base + VALUES]
        if isinstance(label, str) and label.strip().lower().startswith(("т", "t")):
            count += 1
    return count


def read_round(ws):
    rows = list(ws.iter_rows(values_only=True))
    bouts, current, themes_here = [], None, 0
    i = 0
    while i < len(rows):
        row = rows[i]
        head = str(row[0]).strip() if row[0] else ""
        if head.startswith("Бой"):
            current = {"code": head, "teams": []}
            themes_here = theme_count(row)
            bouts.append(current)
            i += 1
            continue
        if not head or current is None:
            i += 1
            continue
        marks_row = rows[i + 1] if i + 1 < len(rows) else ()
        themes, mismatch = [], 0
        for t in range(themes_here):
            base = FIRST + t * STRIDE
            answers = [mark(marks_row[base + k]) if base + k < len(marks_row) else ""
                       for k in range(VALUES)]
            themes.append(answers)
            stated = row[base + VALUES] if base + VALUES < len(row) else None
            if isinstance(stated, (int, float)):
                got = sum(10 * (k + 1) * (1 if a == "right" else -1 if a == "wrong" else 0)
                          for k, a in enumerate(answers))
                if got != round(stated):
                    mismatch += 1
        current["teams"].append({"name": head, "themes": themes,
                                 "total": row[1] if isinstance(row[1], (int, float)) else None,
                                 "place": row[2] if isinstance(row[2], (int, float)) else None,
                                 "mismatch": mismatch})
        i += 2
    return [b for b in bouts if b["teams"]]


wb = openpyxl.load_workbook(SRC, read_only=True, data_only=True)
rounds = {name: read_round(wb[name]) for name in ROUNDS}
wb.close()
bad = sum(t["mismatch"] for bouts in rounds.values() for b in bouts for t in b["teams"])
print("боёв по раундам:", {n: len(b) for n, b in rounds.items()}, "| тем, где сумма не сошлась:", bad)
json.dump(rounds, open("ek-data.json", "w"), ensure_ascii=False)
