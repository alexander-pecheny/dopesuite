"""Turns брейн's parsed бои into a replay transcript.

The whole КИнСБФ: twelve групп of four, six DE pods, a пересев, a group stage of
four threes, another of two fours, and semifinals with a best-of-three final —
132 бои. Only the жеребьёвка is input; every seating after it is derived, so the
replay asserts all of them.

The sheet's group letters run A..L, M..R, S..V, W, X, and the final бои sit on
their own sheet. Each group's бои are in the canon order, which is what lets a
group's four positions be read back out of who met whom.
"""
import json
import sys

# Sheet group letters, block by block, in the order the scheme compiles them.
BLOCKS = [("s1", "ABCDEFGHIJKL"), ("s2", "MNOPQR"), ("s3", "STUV"), ("s4", "WX")]
# Where each бой of a group sits, by its position in that group's sheet order.
# A group of four plays its six бои two per круг; a DE pod plays 2, 2, 1; a group
# of three plays one бой per круг.
SEATING = {6: [(1, 1, 1), (1, 2, 1), (2, 1, 1), (2, 2, 1), (3, 1, 1), (3, 2, 1)],
           5: [(1, 1, 1), (1, 1, 2), (2, 1, 1), (2, 1, 2), (3, 1, 1)],
           3: [(1, 1, 1), (2, 1, 1), (3, 1, 1)]}
# The final sheet, in the order the бои are printed on it.
FINAL = [(1, 1), (1, 2), (3, 1), (2, 1), (2, 2), (2, 3)]


def seed_of(group, position):
    """The seed rank the snake must deal to reach group `group`, seat `position`.

    dope deals 48 seeds into twelve groups as a snake, so group g takes ranks
    g, 25−g, 24+g and 49−g. Running that backwards gives the registration order
    whose deal reproduces the жеребьёвка — the tournament's one input.
    """
    return [group, 25 - group, 24 + group, 49 - group][position - 1]


def positions(bouts):
    """A group of four, read back out of who met whom.

    The canon plays 1-2 and 3-4, then 1-4 and 2-3, then 1-3 and 2-4. So seat 1 is
    whoever is in both the first бой and the third, and the rest follow.
    """
    pair = [{side["name"] for side in bout["teams"]} for bout in bouts]
    first = pair[0] & pair[2]
    if len(first) != 1:
        sys.exit(f"по боям не видно, кто в группе первый: {pair[:3]}")
    p1 = first.pop()
    p2 = (pair[0] - {p1}).pop()
    p4 = (pair[2] - {p1}).pop()
    p3 = (pair[1] - {p4}).pop()
    return [p1, p2, p3, p4]


def roster(stages):
    """The 48 teams in the seed order whose snake deal reproduces the групп."""
    names = [None] * 48
    groups = by_group(stages["1-й групповой этап (протоколы)"])
    for index, letter in enumerate("ABCDEFGHIJKL", 1):
        for seat, name in enumerate(positions(groups[letter]), 1):
            names[seed_of(index, seat) - 1] = name
    holes = [i + 1 for i, name in enumerate(names) if name is None]
    if holes:
        sys.exit(f"в посеве дырки на местах {holes}")
    return names


def by_group(bouts):
    out = {}
    for bout in bouts:
        out.setdefault(bout["group"], []).append(bout)
    return out


def questions(bout, side):
    """One side's questions, comma-separated: mark, then who buzzed."""
    key = ("left", "right")[side]
    out = []
    for question in bout["questions"]:
        cell = question[key]
        letter = {"right": "R", "wrong": "W"}.get(cell["mark"], "")
        if not letter:
            out.append("-")
        elif cell["player"]:
            out.append(f'{letter} {cell["player"]}')
        else:
            out.append(letter)
    return ", ".join(out)


def bout_lines(out, at, bout):
    scores = [side["score"] for side in bout["teams"]]
    # Брейн has no место column: more questions wins, and level is a shared
    # place. Asserting that holds dope to the regламент rather than to itself.
    places = [1 if a > b else 2 if a < b else 1.5 for a, b in (scores, scores[::-1])]
    width = max(len(side["name"]) for side in bout["teams"])
    out.append("")
    out.append(f'# Бой {bout["code"]}')
    out.append(f"[{at}]")
    for side in (0, 1):
        out.append(f'{bout["teams"][side]["name"]:<{width}} | {questions(bout, side)} | '
                   f'{scores[side]:>2} | {places[side]:g}')


def emit(data):
    stages = data["stages"]
    out = ["# брейн (КИнСБФ) СтудЧР-2026 — собрано emit-brain.py из протоколов турнира",
           "[game]", "type: brain", "title: Брейн", "scheme: brain.dsl", "", "[roster]"]
    teams = roster(stages)
    width = max(len(name) for name in teams)
    for number, name in enumerate(teams, 1):
        out.append(f"{number:>2} | {name:<{width}} |")

    sheets = ["1-й групповой этап (протоколы)", "DE (протоколы)",
              "2-й групповой этап (протоколы)", "3-й групповой этап (протоколы)"]
    for (block, letters), sheet in zip(BLOCKS, sheets):
        groups = by_group(stages[sheet])
        for index, letter in enumerate(letters, 1):
            bouts = groups[letter]
            plan = SEATING.get(len(bouts))
            if plan is None:
                sys.exit(f"группа {letter}: {len(bouts)} боёв — не знаю такого расписания")
            for bout, (circle, wave, match) in zip(bouts, plan):
                bout_lines(out, f"{block}/g{index}/r{circle}/w{wave}/m{match}", bout)

    for bout, (circle, match) in zip(stages["Финальный этап (протоколы)"], FINAL):
        bout_lines(out, f"s5/r{circle}/w1/m{match}", bout)
    return "\n".join(out) + "\n"


if __name__ == "__main__":
    sys.stdout.write(emit(json.load(open(sys.argv[1]))))
