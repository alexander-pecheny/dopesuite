"""Transfers ЭК's бои onto its dope game, matching each бой to the one holding
the same teams. Marks only: the per-theme player needs team rosters the fest
does not carry, so Σ and место land, «кто играл» does not."""
import json, sqlite3, sys, urllib.request

DB, BASE = ".tmp/work.db", "http://127.0.0.1:19680"
FEST = int(open(".tmp/ek-fest").read())
token = open(".tmp/token").read().strip()


def api(method, path, payload=None):
    body = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(BASE + path, data=body, method=method,
                                 headers={"Content-Type": "application/json",
                                          "Cookie": "session=" + token})
    with urllib.request.urlopen(req) as resp:
        return resp.status


def run():
    data = json.load(open(".tmp/ek-data.json"))
    db = sqlite3.connect(DB)
    gid = db.execute("select id from games where fest_id=? and game_type='ek'", (FEST,)).fetchone()[0]
    # A finished бой refuses edits, so re-runs skip it: the bracket fills a
    # round at a time as results propagate into the next round's seats.
    by_teams, seats_of = {}, {}
    for match_id, code in db.execute(
            "select id, code from matches where game_id=? and status != 'finished'", (gid,)):
        seats = db.execute("""select p.id, p.name from match_slots ms
            join participants p on p.id = ms.participant_id
            where ms.match_id = ? order by ms.slot_index""", (match_id,)).fetchall()
        if seats:
            by_teams[frozenset(name for _, name in seats)] = code
            seats_of[code] = {name: pid for pid, name in seats}

    done = missing = 0
    for bouts in data.values():
        for bout in bouts:
            names = frozenset(t["name"] for t in bout["teams"])
            code = by_teams.get(names)
            if not code:
                missing += 1
                continue
            ops = []
            for team in bout["teams"]:
                seat = str(seats_of[code][team["name"]])
                for t, answers in enumerate(team["themes"]):
                    for q, value in enumerate(answers):
                        if value:
                            ops.append({"path": ["participants", seat, "themes", t, "answers", q],
                                        "value": value})
                # ЭК ranks its бой by hand — the scorer keeps the host's place —
                # so the sheet's место is entered as the pin it is.
                if team.get("place") is not None:
                    ops.append({"path": ["participants", seat, "pin"], "value": float(team["place"])})
            if ops:
                try:
                    api("PATCH", f"/api/fest/{FEST}/games/{gid}/matches/{code}/state", {"ops": ops})
                except urllib.error.HTTPError as err:
                    print(f"  {bout['code']} -> {code}: {err.code} {err.read()[:120].decode()}")
                    continue
            api("POST", f"/api/fest/{FEST}/games/{gid}/matches/{code}/finish", {"finished": True})
            done += 1
    print(f"перенесено боёв: {done}, ждут результатов: {missing}")
    return done


if __name__ == "__main__":
    run()
