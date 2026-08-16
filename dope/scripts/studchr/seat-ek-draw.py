"""Seats ЭК's later rounds the way the organisers drew them.

After the 1/16 the СтудЧР judges re-drew every round by hand, and that draw is
data, not a rule — dope's bracket carries a template. The Edges stay as they
are (they say where a seat's occupant came from); only who sits there is
replaced by the sheet's own бои."""
import json, sqlite3

DB = ".tmp/work.db"
ROUNDS = {"18": "s1-r2", "14": "s1-r3", "12": "s1-r4", "Финал": "s1-r5"}

data = json.load(open(".tmp/ek-data.json"))
fest = int(open(".tmp/ek-fest").read())
db = sqlite3.connect(DB)
gid = db.execute("select id from games where fest_id=? and game_type='ek'", (fest,)).fetchone()[0]
by_name = {name: pid for pid, name in
           db.execute("select id, name from participants where fest_id=?", (fest,))}

seated = 0
for sheet_round, stage in ROUNDS.items():
    matches = [row[0] for row in db.execute("""select m.id from matches m
        join stages s on s.id = m.stage_id
        where m.game_id=? and s.code=? order by m.position, m.id""", (gid, stage))]
    bouts = data[sheet_round]
    if len(matches) != len(bouts):
        raise SystemExit(f"{stage}: {len(matches)} боёв в dope, {len(bouts)} в листе")
    for match_id, bout in zip(matches, bouts):
        for index, team in enumerate(bout["teams"]):
            pid = by_name.get(team["name"])
            if pid is None:
                raise SystemExit(f"нет участника {team['name']!r}")
            db.execute("update match_slots set participant_id=? where match_id=? and slot_index=?",
                       (pid, match_id, index))
            seated += 1
db.commit()
print("пересажено мест:", seated)
