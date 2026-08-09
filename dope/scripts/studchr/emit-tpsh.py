"""Turns ТПШ's parsed бои into a replay transcript.

ТПШ is one written бой seating all 91 players, then a bracket of 24 that stops
after its second stage — the six left are the winners and there is no final.

Nothing here is a жребий. The отбор seats everyone, and both bracket rounds are
seated from a пересев, so every seating in the tournament is derived and the
replay asserts all of it.
"""
import json
import sys

# Sheet бой codes in play order, one line per stage.
PLAYOFF = [["A", "B", "C", "D", "E", "F"], ["G", "H", "I"]]
MARK = {"": "-", "right": "R", "wrong": "W"}


def marks(themes):
    return " ".join("".join(MARK[cell] for cell in theme) for theme in themes)


def bout(out, at, seats, ranked=True):
    """One бой: its coordinate, then a line per seat.

    A place is pinned only where two seats tie on Σ and the sheet still ranked
    them apart — there the grid cannot imply an order and the hosts settled it
    with a перестрелка.

    The written отбор is unranked. The sheet prints Σ there and no место at all:
    the 1..91 order of «Итоги отбора» is a standings ranking, not a бой's место,
    and dope shares a бой's place between seats that tie. That ranking is checked
    where it decides something — in whom бой A seats.
    """
    width = max(len(seat["name"]) for seat in seats)
    tied, shared = set(), set()
    if ranked:
        tied = {seat["total"] for seat in seats
                if sum(1 for other in seats if other["total"] == seat["total"]) > 1}
        shared = {seat["place"] for seat in seats
                  if sum(1 for other in seats if other["place"] == seat["place"]) > 1}
    out.append(f"[{at}]")
    for seat in seats:
        if seat["total"] is None:
            sys.exit(f"{at}: у {seat['name']} нет Σ — лист недоигран?")
        if not ranked:
            out.append(f'{seat["name"]:<{width}} | {marks(seat["themes"])} | {seat["total"]:>5g} | -')
            continue
        if seat["place"] is None:
            sys.exit(f"{at}: у {seat['name']} нет места — лист недоигран?")
        pin = "!" if seat["total"] in tied and seat["place"] not in shared else ""
        out.append(f'{seat["name"]:<{width}} | {marks(seat["themes"])} | '
                   f'{seat["total"]:>5g} | {seat["place"]:g}{pin}')


def emit(data):
    roster = data["players"]
    out = ["# ТПШ СтудЧР-2026 — собрано emit-tpsh.py из протоколов турнира",
           "[game]", "type: si", "title: ТПШ", "scheme: tpsh.dsl", "", "[roster]"]
    width = max(len(name) for name in roster)
    for number, name in enumerate(roster, 1):
        out.append(f"{number:>2} | {name:<{width}} |")

    out.append("")
    out.append("# Письменный отбор")
    bout(out, "s1/r1/w1/m1", data["written"]["players"], ranked=False)

    playoff = {b["code"]: b for b in data["playoff"]}
    for circle, codes in enumerate(PLAYOFF, 1):
        for within, name in enumerate(codes, 1):
            code = f"Бой {name}"
            if code not in playoff:
                sys.exit(f"в листах нет {code}")
            out.append("")
            out.append(f"# {code}")
            bout(out, f"s2/r{circle}/w1/m{within}", playoff[code]["players"])
    return "\n".join(out) + "\n"


if __name__ == "__main__":
    sys.stdout.write(emit(json.load(open(sys.argv[1]))))
