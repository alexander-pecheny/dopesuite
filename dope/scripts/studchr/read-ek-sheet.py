"""Reads СтудЧР's ЭК book into per-бой marks, составы and player stats.

Each team takes two rows: the first carries its name, Σ, место and the player
who played each theme; the second carries that theme's five marks. Themes
repeat every 7 columns from column 4, and each бой's theme count is however
many «Т…» headers its block names."""
import json, sys, openpyxl

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
        themes, players, mismatch = [], [], 0
        for t in range(themes_here):
            base = FIRST + t * STRIDE
            answers = [mark(marks_row[base + k]) if base + k < len(marks_row) else ""
                       for k in range(VALUES)]
            themes.append(answers)
            player = row[base] if base < len(row) else None
            players.append(str(player).strip() if player else "")
            stated = row[base + VALUES] if base + VALUES < len(row) else None
            if isinstance(stated, (int, float)):
                got = sum(10 * (k + 1) * (1 if a == "right" else -1 if a == "wrong" else 0)
                          for k, a in enumerate(answers))
                if got != round(stated):
                    mismatch += 1
        current["teams"].append({"name": head, "themes": themes, "players": players,
                                 "total": row[1] if isinstance(row[1], (int, float)) else None,
                                 "place": row[2] if isinstance(row[2], (int, float)) else None,
                                 "mismatch": mismatch})
        i += 2
    return [b for b in bouts if b["teams"]]


def read_lineups(ws, playing):
    """«Составы»: a two-row grid, team name over player, one column per player.

    The tab writes two team names in a stray case («вина россии») and lists
    «Аве, Виктория!» twice over, so teams are matched case-insensitively, keep
    the protocols' canonical spelling, and repeats are dropped."""
    rows = list(ws.iter_rows(values_only=True))
    canon = {name.lower(): name for name in playing}
    out = {}
    for column, team in enumerate(rows[0]):
        player = rows[1][column] if column < len(rows[1]) else None
        if not team or not player:
            continue
        name = canon.get(str(team).strip().lower(), str(team).strip())
        players = out.setdefault(name, [])
        if str(player).strip() not in players:
            players.append(str(player).strip())
    return out


def read_stats(ws):
    """«Статистика»: Игрок | Команда | Счёт | Σ+ (plus-темы) | Бои (темы)."""
    out = []
    for row in list(ws.iter_rows(values_only=True))[1:]:
        if not row or not row[0]:
            continue
        out.append({"player": str(row[0]).strip(), "team": str(row[1]).strip(),
                    "sum": int(row[2]), "plus": int(row[3] or 0), "themes": int(row[4])})
    return out


def check_stats(rounds, stats):
    """The tab's aggregates must equal what the decoded marks add up to, or the
    decoding (or the tab) is wrong somewhere."""
    computed = {}
    for bouts in rounds.values():
        for b in bouts:
            for team in b["teams"]:
                for theme, player in zip(team["themes"], team["players"]):
                    if not player:
                        continue
                    entry = computed.setdefault((player, team["name"]), [0, 0, 0])
                    got = sum(10 * (k + 1) * (1 if a == "right" else -1 if a == "wrong" else 0)
                              for k, a in enumerate(theme))
                    entry[0] += got
                    entry[1] += 1 if got > 0 else 0
                    entry[2] += 1
    sheet = {(s["player"], s["team"]): [s["sum"], s["plus"], s["themes"]] for s in stats}
    bad = [key for key in set(computed) | set(sheet) if computed.get(key) != sheet.get(key)]
    if bad:
        sys.exit(f"статистика не сходится с протоколами: {sorted(bad)[:5]} и ещё {max(len(bad) - 5, 0)}")


def read_reseed(ws):
    """«Пересев перед 14», columns K–L: Место | Команда, the twelve of 1/8 ranked."""
    out = []
    for row in ws.iter_rows(min_row=2, min_col=11, max_col=12, values_only=True):
        if row[0] is not None and row[1]:
            out.append([int(row[0]), str(row[1]).strip()])
    return out


wb = openpyxl.load_workbook(SRC, read_only=True, data_only=True)
rounds = {name: read_round(wb[name]) for name in ROUNDS}
reseed = read_reseed(wb["Пересев перед 14"])
playing = {t["name"] for bouts in rounds.values() for b in bouts for t in b["teams"]}
lineups = read_lineups(wb["Составы"], playing)
stats = read_stats(wb["Статистика"])
wb.close()
bad = sum(t["mismatch"] for bouts in rounds.values() for b in bouts for t in b["teams"])
loose = sorted({(t["name"], p) for bouts in rounds.values() for b in bouts for t in b["teams"]
                for p in t["players"] if p and p not in lineups.get(t["name"], [])})
if loose:
    sys.exit(f"игроки тем вне составов: {loose[:5]} и ещё {max(len(loose) - 5, 0)}")
check_stats(rounds, stats)
print("боёв по раундам:", {n: len(b) for n, b in rounds.items()}, "| тем, где сумма не сошлась:", bad)
print("составов:", len(lineups), "| игроков в статистике:", len(stats))
json.dump({"rounds": rounds, "lineups": lineups, "stats": stats, "reseed": reseed},
          open("ek-data.json", "w"), ensure_ascii=False)
