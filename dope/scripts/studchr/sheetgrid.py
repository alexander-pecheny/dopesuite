"""Decoding for СтудЧР's protocol grids, shared by личная СИ and ТПШ.

Both tournaments print the same sheet: a «Бой X» header, then a row per player
holding Σ, место and five cells per theme. The sheets were filled by hand over
two days, so a taken question is written «й» or «q» (the same key on either
layout) or a positive number, and a missed one «ц»/«w» or a negative. A zero or
a blank is a question nobody took.

Every decoded theme is checked against the total the sheet itself computed for
it — that check is the only reason to trust the decoding at all.
"""
RIGHT = {"й", "q", "y", "+"}
WRONG = {"ц", "w", "-"}
THEME_STRIDE, FIRST_VALUE_COL, VALUES = 7, 4, 5

MISREADS = []


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


def number(cell):
    if isinstance(cell, (int, float)):
        return float(cell)
    try:
        return float(str(cell).strip().replace(",", "."))
    except (ValueError, AttributeError):
        return None


def theme_count(header):
    """A бой has as many themes as its header names — six in СИ's group stage,
    eight in its play-off, twelve in the grand final, nine in ТПШ's. Everything
    to the right of them is the statistics block, not marks."""
    count = 0
    for t in range(20):
        base = FIRST_VALUE_COL + t * THEME_STRIDE
        if base + VALUES >= len(header):
            break
        label = header[base + VALUES]
        if isinstance(label, str) and label.strip().startswith("Тема"):
            count += 1
    return count


def read_bouts(ws):
    """Each block is «Бой X» plus its player rows, until a blank line."""
    bouts, current, themes_here, shootout_col = [], None, 0, None
    for row in ws.iter_rows(values_only=True):
        head = str(row[0]).strip() if row[0] else ""
        if head.startswith("Бой ") or head == "Письменный отбор":
            current = {"code": head.split("(")[0].strip(), "players": []}
            themes_here = theme_count(row)
            # Right after the last theme block the play-off sheets keep «П» —
            # the net перестрелка points, outside Σ, breaking the бой's tie.
            shootout_col = FIRST_VALUE_COL + themes_here * THEME_STRIDE
            if shootout_col >= len(row) or str(row[shootout_col]).strip() != "П":
                shootout_col = None
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
            stated = row[base + VALUES]
            if isinstance(stated, (int, float)):
                got = sum((10 * (i + 1)) * (1 if a == "right" else -1 if a == "wrong" else 0)
                          for i, a in enumerate(answers))
                if got != round(stated):
                    MISREADS.append((head, t + 1, got, round(stated),
                                     [str(row[base + i]) for i in range(VALUES)]))
        # Σ and место as the sheet printed them. They are what the replay holds
        # dope against, so they are read verbatim and never recomputed here.
        shootout = number(row[shootout_col]) if shootout_col is not None else None
        current["players"].append({"name": head, "themes": themes,
                                   "total": number(row[1]), "place": number(row[2]),
                                   "shootout": int(shootout) if shootout else 0})
    return [b for b in bouts if b["players"]]
