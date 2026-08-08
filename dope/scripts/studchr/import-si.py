"""Transfers СтудЧР's личная СИ onto dope: the real 54 players in the seed order
that deals them into the sheets' own groups, then every бой's marks.

Бои are matched by who sat at them, not by code — the sheets and dope both deal
a snake, but identity is the join key that cannot drift (session decision)."""
import json, sqlite3, sys, urllib.request

DB, FEST, BASE = ".tmp/work.db", 14, "http://127.0.0.1:19680"
data = json.load(open(".tmp/si-data.json"))
token = open(".tmp/token").read().strip()


def seed_order():
    """The registration order whose snake deal reproduces the sheets' groups:
    row k of group g takes seed rank 6k + (g on even rows, 7−g on odd)."""
    names = [None] * 54
    for gi, letter in enumerate("ABCDEF"):
        for k, player in enumerate(data["groups"][letter]):
            column = gi + 1 if k % 2 == 0 else 6 - gi
            names[6 * k + column - 1] = player
    missing = [i for i, n in enumerate(names) if n is None]
    if missing:
        sys.exit(f"seed order has holes at {missing}")
    return names


def split(full):
    parts = full.split(None, 1)
    return (parts[0], parts[1]) if len(parts) == 2 else (full, "")


if __name__ == "__main__":
    db = sqlite3.connect(DB)
    db.execute("delete from fest_players where fest_id = ?", (FEST,))
    for name in seed_order():
        first, last = split(name)
        db.execute("insert into fest_players(fest_id, first_name, last_name) values(?, ?, ?)",
                   (FEST, first, last))
    db.commit()
    print("roster:", db.execute("select count(*) from fest_players where fest_id=?", (FEST,)).fetchone()[0])


def api(method, path, payload=None):
    body = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(BASE + path, data=body, method=method,
                                 headers={"Content-Type": "application/json", "Cookie": "session=" + token})
    with urllib.request.urlopen(req) as resp:
        return resp.status, resp.read()


def import_bouts():
    """Seats are matched by who occupies them, so a бой lands on the dope match
    that already holds exactly its players, whatever either side calls it."""
    db = sqlite3.connect(DB)
    gid = db.execute("select id from games where fest_id=? and game_type='si'", (FEST,)).fetchone()[0]
    by_players, seat_index = {}, {}
    for match_id, code in db.execute("select id, code from matches where game_id=?", (gid,)):
        seats = db.execute("""select ms.slot_index, p.name from match_slots ms
            join participants p on p.id = ms.participant_id
            where ms.match_id = ? order by ms.slot_index""", (match_id,)).fetchall()
        if not seats:
            continue
        by_players[frozenset(name for _, name in seats)] = code
        seat_index[code] = {name: index for index, name in seats}

    sheets = [b for round_bouts in data["rounds"].values() for b in round_bouts]
    done = missing = 0
    for bout in sheets:
        names = frozenset(p["name"] for p in bout["players"])
        code = by_players.get(names)
        if not code:
            missing += 1
            print("нет боя для", bout["code"], sorted(names))
            continue
        seats = seat_index[code]
        ops = []
        for player in bout["players"]:
            seat = seats[player["name"]]
            for t, answers in enumerate(player["themes"]):
                for q, value in enumerate(answers):
                    if value:
                        ops.append({"path": ["themes", t, "answers", seat, q], "value": value})
        if ops:
            status, body = api("PATCH", f"/api/fest/{FEST}/games/{gid}/matches/{code}/state", {"ops": ops})
            if status != 200:
                sys.exit(f"{bout['code']} -> {code}: {status} {body[:200]}")
        api("POST", f"/api/fest/{FEST}/games/{gid}/matches/{code}/finish", {"finished": True})
        done += 1
    print(f"перенесено боёв: {done}, без пары: {missing}")
