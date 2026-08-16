"""Writes ЭК's hand draw into the slots' Edges.

After the 1/16 the СтудЧР judges re-drew each round by hand. Seating those
teams directly does not survive: the resolver re-seats every slot from its Edge
whenever an earlier round is recomputed. So the draw goes where it belongs —
each seat's source_ref names the бой and place its occupant came from, exactly
as the sheets drew it, and the resolver then fills it itself."""
import json, sqlite3

DB = ".tmp/work.db"
ORDER = ["116", "18", "14", "12", "Финал"]
STAGES = {"116": "s1-r1", "18": "s1-r2", "14": "s1-r3", "12": "s1-r4", "Финал": "s1-r5"}

data = json.load(open(".tmp/ek-data.json"))
fest = int(open(".tmp/ek-fest").read())
db = sqlite3.connect(DB)
gid = db.execute("select id from games where fest_id=? and game_type='ek'", (fest,)).fetchone()[0]

# бой code in dope for each sheet бой, round by round, in sheet order.
codes = {}
for sheet_round, stage in STAGES.items():
    rows = db.execute("""select m.code from matches m join stages s on s.id = m.stage_id
        where m.game_id=? and s.code=? order by m.position, m.id""", (gid, stage)).fetchall()
    codes[sheet_round] = [r[0] for r in rows]
    if len(rows) != len(data[sheet_round]):
        raise SystemExit(f"{stage}: {len(rows)} боёв, в листе {len(data[sheet_round])}")

# Where each team finished in the previous round: its бой code and its place.
written = 0
for prev, cur in zip(ORDER, ORDER[1:]):
    came_from = {}
    for code, bout in zip(codes[prev], data[prev]):
        for team in bout["teams"]:
            if team.get("place") is not None:
                came_from[team["name"]] = (code, float(team["place"]))
    for code, bout in zip(codes[cur], data[cur]):
        match_id = db.execute("select id from matches where game_id=? and code=?", (gid, code)).fetchone()[0]
        for index, team in enumerate(bout["teams"]):
            source = came_from.get(team["name"])
            if source is None:
                raise SystemExit(f"{code}: откуда взялась {team['name']!r}?")
            ref = json.dumps({"match": source[0], "place": int(source[1]),
                              "label": f"{source[0]}, м. {int(source[1])}"}, ensure_ascii=False)
            db.execute("""update match_slots set source_type='from_match', source_ref_json=?,
                participant_id=null where match_id=? and slot_index=?""", (ref, match_id, index))
            written += 1
db.commit()
print("переписано рёбер:", written)
