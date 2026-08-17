"""Turns личная СИ's parsed бои into a replay transcript.

The counterpart to emit-ek.py: read-si-sheets.py knows the workbook, this knows
the transcript, and nothing downstream knows either. See
dope/docs/replay-transcript.md.

Nothing here is a жребий. The group stage is dealt from the fest's seeding and
the play-off from a пересев after every round, so every seating in the tournament
is derived and the replay asserts all of it. That is the point of writing the
roster in seed order rather than alphabetically: it is the one input, and the
seventy-two group бои and the whole bracket follow from it.
"""
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from transcript import table

GROUPS = "ABCDEF"
# Sheet бой codes in play order, one line per round of the play-off.
PLAYOFF = [["G", "H", "I", "J", "K", "L"],
           ["M", "N", "O", "P", "Q", "R"],
           ["S", "T", "U", "V", "W"],
           ["X", "Y", "Z"],
           ["AA", "AB"],
           ["AC"],
           ["AD"]]
MARK = {"": "-", "right": "R", "wrong": "W"}


def marks(themes):
    return " ".join("".join(MARK[cell] for cell in theme) for theme in themes)


def place(value):
    return f"{value:g}"


def seed_order(groups):
    """The registration order whose snake deal reproduces the sheets' groups.

    Row k of group g takes seed rank 6k + (g on even rows, 7−g on odd) — the
    inverse of the snake dope deals with. The sheets' own «Регистрация» numbers
    do not reproduce the sheets' own groups, so the draw is recorded as the
    seeding that does, which is what a draw is: input.
    """
    names = [None] * 54
    for index, letter in enumerate(GROUPS):
        for k, player in enumerate(groups[letter]):
            column = index + 1 if k % 2 == 0 else 6 - index
            names[6 * k + column - 1] = player
    holes = [i + 1 for i, name in enumerate(names) if name is None]
    if holes:
        sys.exit(f"в посеве дырки на местах {holes}")
    return names


def bout(out, at, seats):
    """One бой: its coordinate, then a line per seat.

    Nothing is pinned. Where two seats tie on Σ the sheet's «П» carries the
    перестрелка that split them; it rides along as its own lines, dope ranks
    with it, and the sheet's места stay assertions.
    """
    width = max(len(seat["name"]) for seat in seats)
    out.append(f"[{at}]")
    for seat in seats:
        if seat["total"] is None or seat["place"] is None:
            sys.exit(f"{at}: у {seat['name']} нет Σ или места — лист недоигран?")
        out.append(f'{seat["name"]:<{width}} | {marks(seat["themes"])} | '
                   f'{seat["total"]:>5g} | {place(seat["place"])}')
    for seat in seats:
        if seat.get("shootout"):
            out.append(f'перестрелка {seat["name"]}: {seat["shootout"]}')


def emit(data):
    roster = seed_order(data["groups"])
    out = ["# личная СИ СтудЧР-2026 — собрано emit-si.py из протоколов турнира",
           "[game]", "type: si", "title: СИ", "scheme: si.dsl", "", "[roster]"]
    width = max(len(name) for name in roster)
    for number, name in enumerate(roster, 1):
        out.append(f"{number:>2} | {name:<{width}} |")

    # Group stage. Each group holds one стол and plays twelve бои there over
    # four круги, so the three бои of a круг are three заходы rather than three
    # tables at once. The sheet numbers them A1..A12 in exactly that order.
    by_code = {b["code"]: b for round_bouts in data["rounds"].values() for b in round_bouts}
    for index, letter in enumerate(GROUPS):
        for n in range(1, 13):
            code = f"Бой {letter}{n}"
            if code not in by_code:
                sys.exit(f"в листах нет {code}")
            circle, wave = divmod(n - 1, 3)
            out.append("")
            out.append(f"# {code}")
            bout(out, f"s1/g{index + 1}/r{circle + 1}/w{wave + 1}/m1",
                 by_code[code]["players"])
        # The group's table as «Группы A-B» sorts it: no места printed, so the
        # place is the row — a pair level on every count is the sheet's call.
        table(out, f"s1/g{index + 1}", list(enumerate(data["tables"][letter], 1)))

    playoff = {b["code"]: b for b in data["playoff"]}
    for circle, codes in enumerate(PLAYOFF, 1):
        for within, name in enumerate(codes, 1):
            code = f"Бой {name}"
            if code not in playoff:
                sys.exit(f"в листах нет {code}")
            out.append("")
            out.append(f"# {code}")
            bout(out, f"s2/r{circle}/w1/m{within}", playoff[code]["players"])

    # The sheet's own aggregates: Счёт, Без − and Бои per player. One line the
    # sheet cannot justify from its own protocols — reviewed and overridden.
    out += ["", "[статистика]"]
    stats = data["stats"]
    width = max(len(s["player"]) for s in stats)
    for s in stats:
        out.append(f'{s["player"]:<{width}} | {s["sum"]:>4} | {s["plus"]:>4} | {s["bouts"]:>2}')
    out += ["", "override [статистика] Σ+ Станислав Хамидулин: "
            "правка руками в живом листе — взятую двадцатку перенесли из его строки в строку Матвеевой (Круг 3, бой B7, тема 5), "
            "и формулы Σ+ и счёта двадцаток (AU19, AY19) уехали за ячейкой в чужую строку; лист считает её обоим, Σ сходится в обе стороны"]
    return "\n".join(out) + "\n"


if __name__ == "__main__":
    sys.stdout.write(emit(json.load(open(sys.argv[1]))))
