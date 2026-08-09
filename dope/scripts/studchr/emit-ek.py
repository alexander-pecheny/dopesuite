"""Turns the ЭК sheet's parsed бои into a replay transcript.

This is the whole seam between a tournament's flavour of Google Sheets and the
replayer: read-ek-sheet.py knows the workbook, this knows the transcript, and
nothing downstream knows either. See dope/docs/replay-transcript.md.

СтудЧР's ЭК ran 1/16 финала on six столов, so that round is two заходов; every
later round fits one. Rounds after the first were re-drawn by hand, so each of
them is a жребий — the seating is input, not something the resolver derives.
"""
import json
import sys

# Sheet round key → (круг, how many заходов it took).
ROUNDS = [("116", 1, 2), ("18", 2, 1), ("14", 3, 1), ("12", 4, 1), ("Финал", 5, 1)]
MARK = {"": "-", "right": "R", "wrong": "W"}


def marks(themes):
    return " ".join("".join(MARK[cell] for cell in theme) for theme in themes)


def place(value):
    if value is None:
        return None
    return f"{value:g}"


def entrants(data, registry):
    """Who actually played ЭК, in fest-registration order.

    The roster is derived from the бои rather than copied from the fest, because
    ЭК seated 48 of the fest's 65 teams and a scheme that says `teams: 48`
    rightly refuses 65. Numbers are the fest's registration numbers — the ЭК
    sheet's own numbering was not transcribed, and inventing one would be worse
    than reusing a real one.
    """
    played = {seat["name"] for bouts in data.values() for bout in bouts for seat in bout["teams"]}
    known = {team["name"]: team for team in registry}
    missing = sorted(played - set(known))
    if missing:
        sys.exit(f"нет в реестре феста: {missing}")
    return sorted((known[name] for name in played), key=lambda t: t["number"])


def emit(data, registry):
    roster = entrants(data, registry)
    out = ["# ЭК СтудЧР-2026 — собрано emit-ek.py из протоколов турнира",
           "[game]", "type: ek", "title: ЭК", "scheme: ek.dsl", "", "[roster]"]
    width = max(len(team["name"]) for team in roster)
    for team in roster:
        out.append(f'{team["number"]:>2} | {team["name"]:<{width}} | {team.get("city", "")}')

    for key, circle, waves in ROUNDS:
        bouts = data[key]
        per_wave = len(bouts) // waves
        for index, bout in enumerate(bouts):
            wave, within = divmod(index, per_wave)
            seats = bout["teams"]
            names = max(len(seat["name"]) for seat in seats)
            out.append("")
            out.append(f"# {bout['code']}")
            out.append(f"[s1/r{circle}/w{wave + 1}/m{within + 1}] жребий")
            # Место in this workbook is typed, not computed — column C holds
            # literal numbers. Where two teams tie on Σ the marks cannot imply an
            # order at all, so that place is input (`3!`): the hosts settled it
            # with a перестрелка, whose material the protocol grid never records.
            # Untied places stay assertions, and dope has to derive them.
            tied = {seat["total"] for seat in seats
                    if sum(1 for other in seats if other["total"] == seat["total"]) > 1}
            for seat in seats:
                if place(seat.get("place")) is None:
                    sys.exit(f"{bout['code']}: у {seat['name']} нет места — лист недоигран?")
                pin = "!" if seat["total"] in tied else ""
                out.append(f'{seat["name"]:<{names}} | {marks(seat["themes"])} | '
                           f'{seat["total"]:>4} | {place(seat["place"])}{pin}')
    return "\n".join(out) + "\n"


if __name__ == "__main__":
    data = json.load(open(sys.argv[1]))
    roster = json.load(open(sys.argv[2]))["teams"]
    sys.stdout.write(emit(data, roster))
