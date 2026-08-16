#!/bin/zsh
# Rebuilds the local СтудЧР working copy from a dopetest snapshot and creates
# all five games from their schemes, through the real host handlers.
set -e
cd /home/pecheny/.herdr/worktrees/dopesuite/dope-brain
T=.tmp
[ -f $T/work.pid ] && kill $(cat $T/work.pid) 2>/dev/null || true
sleep 1
rm -f $T/work.db $T/work.db-wal $T/work.db-shm
cp $T/dopetest.db $T/work.db
python3 - <<'PY'
import sqlite3, hashlib, datetime
db = sqlite3.connect(".tmp/work.db")
token = open(".tmp/token").read()
now = datetime.datetime.now(datetime.timezone.utc)
fmt = lambda d: d.strftime("%Y-%m-%dT%H:%M:%SZ")
db.execute("insert into sessions(user_id, token_hash, created_at, expires_at, last_seen_at) values(1, ?, ?, ?, ?)",
           (hashlib.sha256(token.encode()).hexdigest(), fmt(now), fmt(now + datetime.timedelta(days=30)), fmt(now)))
db.execute("insert or ignore into fest_organizers(fest_id, user_id, role, added_at) values(14, 1, 'admin', ?)", (fmt(now),))
import importlib.util
spec = importlib.util.spec_from_file_location("imp_si", ".tmp/import-si.py")
mod = importlib.util.module_from_spec(spec); spec.loader.exec_module(mod)
rows = []
for name in mod.seed_order():
    first, last = mod.split(name)
    rows.append((14, first, last))
db.executemany("insert into fest_players(fest_id, first_name, last_name) values(?, ?, ?)", rows)
db.commit()
PY
(cd $T && DOPE_DB=$PWD/work.db PORT=19680 ./dope-server > work.log 2>&1 & echo $! > work.pid)
sleep 14
TOKEN=$(cat $T/token); B=http://127.0.0.1:19680
post() { curl -s -o /dev/null -w '%{http_code} ' -b "session=$TOKEN" "$@" "$B/host/fest/14/game/new"; }
post --data-urlencode "game_type=si" --data-urlencode "brain_dsl=$(cat $T/si.dsl)"; echo "СИ"
post --data-urlencode "game_type=ek" --data-urlencode "brain_dsl=$(cat $T/ek.dsl)"; echo "ЭК"
post -d "game_type=od" -d "od_tours=6" -d "od_questions=15"; echo "ОД"
python3 - <<'SEED'
import json, importlib.util, sqlite3
spec = importlib.util.spec_from_file_location("flat", ".tmp/import-flat.py")
flat = importlib.util.module_from_spec(spec); spec.loader.exec_module(flat)
db = sqlite3.connect(".tmp/work.db")
gid = db.execute("select id from games where fest_id=14 and game_type='od'").fetchone()[0]
flat.seat_od_teams(".tmp/work.db", gid, json.load(open(".tmp/od-data.json"))["teams"])
SEED
post -d "game_type=ksi" -d "ksi_themes=20"; echo "КСИ"
