#!/usr/bin/env python3
"""Drive watchable, realistic live edits across an OD, a KSI, and an EK game so
you can open the public viewer pages and see state change in real time.

This reuses the EXISTING public `test` fest (which already has valid OD/KSI/EK
data with real teams) rather than cloning its bracket — EK's slots/results FK
into the teams graph, so a faithful clone would be huge. To stay fully
reversible, `setup` snapshots everything the simulator touches and `teardown`
restores it exactly, then removes the temporary editor account.

Three commands:

  # on the VPS (needs the DB): snapshot + mint a temp organizer session
  python3 realtime_demo.py setup --db /var/lib/dope/fest.db --fest 3

  # anywhere (HTTP to prod): drive live edits for a while
  python3 realtime_demo.py simulate --base https://dope.pecheny.me \
      --fest 3 --od 1 --ksi 2 --ek 3 --ek-match A --token <tok> --duration 120

  # on the VPS: restore the fest to its pre-demo state
  python3 realtime_demo.py teardown --db /var/lib/dope/fest.db --stamp <stamp>

Edits stay schema-valid: OD only changes `entries` scores (the `teams` key is
frozen for OD), KSI only toggles answer marks (`participants` frozen), and EK
marks answers in one match via the match-update endpoint.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import random
import secrets
import sqlite3
import sys
import time
import urllib.error
import urllib.request


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def default_stamp() -> str:
    return dt.datetime.now(dt.timezone.utc).strftime("%y%m%d-%H%M%S")


def hash_token(token: str) -> str:
    return hashlib.sha256(token.encode("ascii")).hexdigest()


def backup_path(db_path: str, stamp: str) -> str:
    return os.path.join(os.path.dirname(os.path.abspath(db_path)), f"loadtest_demo_{stamp}.json")


# ---------------------------------------------------------------- setup -----

def connect(db_path: str) -> sqlite3.Connection:
    con = sqlite3.connect(db_path, timeout=15)
    con.row_factory = sqlite3.Row
    con.execute("PRAGMA foreign_keys = ON")
    con.execute("PRAGMA busy_timeout = 15000")
    return con


def games_by_type(con: sqlite3.Connection, fest_id: int) -> dict:
    out = {}
    for row in con.execute("select id, game_type from games where fest_id=?", (fest_id,)):
        out.setdefault(row["game_type"], row["id"])
    return out


def first_populated_match(con: sqlite3.Connection, ek_game: int) -> str | None:
    row = con.execute(
        """select m.code
             from matches m
            where m.game_id=?
              and (select count(*) from match_results r where r.match_id=m.id) > 0
              and (select count(*) from match_slots s where s.match_id=m.id and s.team_id is not null) >= 2
         order by (select count(*) from match_slots s where s.match_id=m.id and s.team_id is not null) desc,
                  m.id
            limit 1""",
        (ek_game,),
    ).fetchone()
    return row["code"] if row else None


def rows(con: sqlite3.Connection, sql: str, args=()) -> list:
    return [dict(r) for r in con.execute(sql, args)]


def setup(con: sqlite3.Connection, db_path: str, fest_id: int, stamp: str, expiry_days: int) -> dict:
    types = games_by_type(con, fest_id)
    od, ksi, ek = types.get("od"), types.get("ksi"), types.get("ek")
    if not (od and ksi and ek):
        raise SystemExit(f"fest {fest_id} is missing od/ksi/ek games: {types}")
    ek_match = first_populated_match(con, ek)
    if not ek_match:
        raise SystemExit(f"no populated EK match found in game {ek}")

    game_ids = [od, ksi, ek]
    qmarks = ",".join("?" for _ in game_ids)

    snapshot = {
        "stamp": stamp,
        "fest_id": fest_id,
        "max_event_id": (con.execute("select coalesce(max(id),0) m from events where fest_id=?", (fest_id,)).fetchone()["m"]),
        "fest_revision": con.execute("select revision from fests where id=?", (fest_id,)).fetchone()["revision"],
        "games": rows(con, f"select id, state_json, revision, updated_at from games where id in ({qmarks})", game_ids),
        # Snapshot the whole EK bracket's mutable state so restore is exact
        # regardless of any downstream cascade from editing a result.
        "stages": rows(con, "select id, status from stages where game_id=?", (ek,)),
        "matches": rows(con, "select id, status, revision from matches where game_id=?", (ek,)),
        "match_slots": rows(con, "select * from match_slots where match_id in (select id from matches where game_id=?)", (ek,)),
        "match_results": rows(con, "select * from match_results where match_id in (select id from matches where game_id=?)", (ek,)),
    }

    # Temp organizer + injected session so the simulator can edit.
    now = utc_now()
    username = f"lt_demo_{stamp}"
    cur = con.cursor()
    cur.execute(
        "insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at) "
        "values(null, null, ?, 0, ?, ?)",
        (username, now, now),
    )
    uid = cur.lastrowid
    token = secrets.token_hex(32)
    expires = (dt.datetime.now(dt.timezone.utc) + dt.timedelta(days=expiry_days)).strftime("%Y-%m-%dT%H:%M:%SZ")
    cur.execute(
        "insert into sessions(user_id, token_hash, created_at, expires_at, last_seen_at) values(?, ?, ?, ?, ?)",
        (uid, hash_token(token), now, expires, now),
    )
    cur.execute(
        "insert into fest_organizers(fest_id, user_id, role, added_at) values(?, ?, 'admin', ?)",
        (fest_id, uid, now),
    )
    snapshot["temp_user_id"] = uid

    with open(backup_path(db_path, stamp), "w") as f:
        json.dump(snapshot, f)
    con.commit()

    return {
        "stamp": stamp,
        "fest_id": fest_id,
        "od_game": od,
        "ksi_game": ksi,
        "ek_game": ek,
        "ek_match": ek_match,
        "token": token,
        "backup_file": backup_path(db_path, stamp),
        "viewer_urls": {
            "od": "/fest/test/game/od/",
            "ksi": "/fest/test/game/ksi/",
            "ek": "/fest/test/game/ek/",
        },
    }


def teardown(con: sqlite3.Connection, db_path: str, stamp: str) -> dict:
    path = backup_path(db_path, stamp)
    if not os.path.exists(path):
        raise SystemExit(f"backup not found: {path}")
    with open(path) as f:
        snap = json.load(f)
    fest_id = snap["fest_id"]
    cur = con.cursor()

    # Restore game state.
    for g in snap["games"]:
        cur.execute(
            "update games set state_json=?, revision=?, updated_at=? where id=?",
            (g["state_json"], g["revision"], g["updated_at"], g["id"]),
        )

    # Restore the EK bracket: wipe and re-insert slots + results, reset statuses.
    ek_match_ids = [m["id"] for m in snap["matches"]]
    if ek_match_ids:
        qm = ",".join("?" for _ in ek_match_ids)
        cur.execute(f"delete from match_results where match_id in ({qm})", ek_match_ids)
        cur.execute(f"delete from match_slots where match_id in ({qm})", ek_match_ids)
    for s in snap["match_slots"]:
        cols = ",".join(s.keys())
        cur.execute(f"insert into match_slots ({cols}) values ({','.join('?' for _ in s)})", list(s.values()))
    for r in snap["match_results"]:
        cols = ",".join(r.keys())
        cur.execute(f"insert into match_results ({cols}) values ({','.join('?' for _ in r)})", list(r.values()))
    for m in snap["matches"]:
        cur.execute("update matches set status=?, revision=? where id=?", (m["status"], m["revision"], m["id"]))
    for st in snap["stages"]:
        cur.execute("update stages set status=? where id=?", (st["status"], st["id"]))

    cur.execute("update fests set revision=? where id=?", (snap["fest_revision"], fest_id))
    cur.execute("delete from events where fest_id=? and id>?", (fest_id, snap["max_event_id"]))
    cur.execute("delete from users where id=?", (snap["temp_user_id"],))  # cascades sessions
    cur.execute("delete from fest_organizers where user_id=?", (snap["temp_user_id"],))
    con.commit()
    os.remove(path)
    return {"restored_games": len(snap["games"]), "restored_slots": len(snap["match_slots"]),
            "restored_results": len(snap["match_results"]), "removed_user": snap["temp_user_id"]}


# ------------------------------------------------------------- simulate -----

class Client:
    def __init__(self, base: str, token: str):
        self.base = base.rstrip("/")
        self.token = token

    def _req(self, method: str, path: str, body: bytes | None) -> bytes:
        req = urllib.request.Request(self.base + path, data=body, method=method)
        req.add_header("Cookie", f"session={self.token}")
        if body is not None:
            req.add_header("Content-Type", "application/json")
        with urllib.request.urlopen(req, timeout=30) as resp:
            return resp.read()

    def get_json(self, path: str):
        return json.loads(self._req("GET", path, None))

    def put_json(self, path: str, obj) -> None:
        self._req("PUT", path, json.dumps(obj).encode())

    def post_json(self, path: str, obj) -> None:
        self._req("POST", path, json.dumps(obj).encode())


def simulate(base: str, fest: int, od: int, ksi: int, ek: int, ek_match: str,
             token: str, duration: float, interval: float) -> None:
    rng = random.Random()
    c = Client(base, token)

    # Pull current state once; we are the only editor, so we mutate local copies
    # and PUT them back (OD/KSI replace the whole state blob each edit).
    od_state = c.get_json(f"/api/fest/{fest}/games/{od}/state")
    ksi_state = c.get_json(f"/api/fest/{fest}/games/{ksi}/state")
    ek_view = c.get_json(f"/api/fest/{fest}/games/{ek}/matches/{ek_match}")
    n_themes = len(ek_view["teams"][0]["themes"])
    n_answers = len(ek_view["teams"][0]["themes"][0]["answers"])
    n_ek_teams = len(ek_view["teams"])

    n_entries = len(od_state.get("entries", []))
    n_od_teams = len(od_state.get("teams", []))
    n_ksi_themes = len(ksi_state.get("themes", []))
    n_participants = len(ksi_state.get("participants", []))
    n_ksi_answers = len(ksi_state["themes"][0]["answers"][0]) if n_ksi_themes else 0

    print(f"simulating: OD({n_entries}x{n_od_teams}) KSI({n_ksi_themes}t x{n_participants}p) "
          f"EK match {ek_match}({n_ek_teams}t x{n_themes}th x{n_answers}a) for {duration:.0f}s", flush=True)

    # EK marks can only be edited on an active match; reopen it for the demo.
    # teardown restores its finished status from the snapshot.
    try:
        c.post_json(f"/api/fest/{fest}/games/{ek}/matches/{ek_match}/update", {"finished": False})
    except Exception as e:  # noqa: BLE001
        print(f"  warning: could not reopen EK match {ek_match}: {e}", flush=True)

    od_marks = [0, 10, 20, 30, 40, 50]
    deadline = time.monotonic() + duration
    edits = {"od": 0, "ksi": 0, "ek": 0}
    errors = 0
    nxt = 0
    while time.monotonic() < deadline:
        which = ("od", "ksi", "ek")[nxt % 3]
        nxt += 1
        try:
            if which == "od" and n_entries and n_od_teams:
                r = rng.randrange(n_entries)
                j = rng.randrange(n_od_teams)
                od_state["entries"][r][j] = rng.choice(od_marks)
                c.put_json(f"/api/fest/{fest}/games/{od}/state", od_state)
                edits["od"] += 1
            elif which == "ksi" and n_ksi_themes and n_participants:
                t = rng.randrange(n_ksi_themes)
                p = rng.randrange(n_participants)
                q = rng.randrange(n_ksi_answers)
                cur = ksi_state["themes"][t]["answers"][p][q]
                ksi_state["themes"][t]["answers"][p][q] = "wrong" if cur == "right" else "right"
                c.put_json(f"/api/fest/{fest}/games/{ksi}/state", ksi_state)
                edits["ksi"] += 1
            else:
                payload = {
                    "team": rng.randrange(n_ek_teams),
                    "theme": rng.randrange(n_themes),
                    "answer": rng.randrange(n_answers),
                    "mark": rng.choice(["right", "wrong", ""]),
                }
                c.post_json(f"/api/fest/{fest}/games/{ek}/matches/{ek_match}/update", payload)
                edits["ek"] += 1
        except urllib.error.HTTPError as e:
            errors += 1
            print(f"\n  edit error ({which}): {e.code} {e.reason}", flush=True)
        except Exception as e:  # noqa: BLE001 — surface anything else but keep going
            errors += 1
            print(f"\n  edit error ({which}): {e}", flush=True)
        print(f"\r  edits od={edits['od']} ksi={edits['ksi']} ek={edits['ek']} errors={errors}   ", end="", flush=True)
        time.sleep(interval)
    print(f"\ndone: {edits}, errors={errors}", flush=True)


# ----------------------------------------------------------------- main -----

def main() -> int:
    p = argparse.ArgumentParser(description=__doc__)
    sub = p.add_subparsers(dest="cmd", required=True)

    s = sub.add_parser("setup")
    s.add_argument("--db", default="/var/lib/dope/fest.db")
    s.add_argument("--fest", type=int, required=True)
    s.add_argument("--stamp", default=None)
    s.add_argument("--expiry-days", type=int, default=1)

    t = sub.add_parser("teardown")
    t.add_argument("--db", default="/var/lib/dope/fest.db")
    t.add_argument("--stamp", required=True)

    sim = sub.add_parser("simulate")
    sim.add_argument("--base", default="https://dope.pecheny.me")
    sim.add_argument("--fest", type=int, required=True)
    sim.add_argument("--od", type=int, required=True)
    sim.add_argument("--ksi", type=int, required=True)
    sim.add_argument("--ek", type=int, required=True)
    sim.add_argument("--ek-match", required=True)
    sim.add_argument("--token", required=True)
    sim.add_argument("--duration", type=float, default=120.0)
    sim.add_argument("--interval", type=float, default=0.7, help="seconds between edits (rotates od/ksi/ek)")

    args = p.parse_args()

    if args.cmd == "setup":
        con = connect(args.db)
        try:
            print(json.dumps(setup(con, args.db, args.fest, args.stamp or default_stamp(), args.expiry_days)))
        finally:
            con.close()
    elif args.cmd == "teardown":
        con = connect(args.db)
        try:
            print(json.dumps(teardown(con, args.db, args.stamp)))
        finally:
            con.close()
    else:
        simulate(args.base, args.fest, args.od, args.ksi, args.ek, args.ek_match,
                 args.token, args.duration, args.interval)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
