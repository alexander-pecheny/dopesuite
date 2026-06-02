#!/usr/bin/env python3
"""Provision (and tear down) a disposable public fest for load testing.

Run this ON THE VPS against the live SQLite file. It seeds one public fest with
a single editable game and N organizer accounts, then injects a session row for
each account directly (sessions are just sha256(token), so no login round trip
is needed). It prints a JSON blob the Go load driver consumes via its flags.

Everything it creates is tagged by the fest slug `dope-loadtest-<stamp>` and the
usernames `lt_<stamp>_<i>`, so teardown is an exact, reversible delete.

    # on the VPS
    python3 provision.py provision --db /var/lib/dope/fest.db --editors 3
    # ... copy the printed slug / fest_id / game_id / tokens to the Go driver ...
    python3 provision.py teardown  --db /var/lib/dope/fest.db --stamp 260602-1530

SQLite is opened with a busy_timeout so these writes interleave safely with the
running server's WAL connection.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import secrets
import sqlite3
import sys


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def utc_in_days(days: int) -> str:
    return (dt.datetime.now(dt.timezone.utc) + dt.timedelta(days=days)).strftime(
        "%Y-%m-%dT%H:%M:%SZ"
    )


def default_stamp() -> str:
    return dt.datetime.now(dt.timezone.utc).strftime("%y%m%d-%H%M")


def connect(db_path: str) -> sqlite3.Connection:
    con = sqlite3.connect(db_path, timeout=10)
    con.execute("PRAGMA foreign_keys = ON")
    con.execute("PRAGMA busy_timeout = 10000")
    return con


def hash_token(token: str) -> str:
    # Matches dope's hashSessionToken: hex(sha256(token)).
    return hashlib.sha256(token.encode("ascii")).hexdigest()


def provision(con: sqlite3.Connection, stamp: str, editors: int, expiry_days: int) -> dict:
    now = utc_now()
    slug = f"dope-loadtest-{stamp}"
    cur = con.cursor()

    if cur.execute("select 1 from fests where slug = ?", (slug,)).fetchone():
        raise SystemExit(f"fest {slug!r} already exists; tear it down first")

    # 1. Editor accounts (no password — we inject sessions directly).
    user_ids: list[int] = []
    tokens: list[str] = []
    for i in range(editors):
        username = f"lt_{stamp}_{i}"
        cur.execute(
            "insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at) "
            "values(null, null, ?, 0, ?, ?)",
            (username, now, now),
        )
        uid = cur.lastrowid
        user_ids.append(uid)

        token = secrets.token_hex(32)
        tokens.append(token)
        cur.execute(
            "insert into sessions(user_id, token_hash, created_at, expires_at, last_seen_at) "
            "values(?, ?, ?, ?, ?)",
            (uid, hash_token(token), now, utc_in_days(expiry_days), now),
        )

    # 2. The disposable public fest, owned by the first editor.
    cur.execute(
        "insert into fests(slug, title, description, created_by, revision, created_at, updated_at, is_public) "
        "values(?, ?, ?, ?, 1, ?, ?, 1)",
        (slug, f"Load Test {stamp}", "Disposable fest for perf testing.", user_ids[0], now, now),
    )
    fest_id = cur.lastrowid

    # 3. Grant every editor an admin role on the fest (admin can edit game tables).
    for idx, uid in enumerate(user_ids):
        role = "creator" if idx == 0 else "admin"
        cur.execute(
            "insert into fest_organizers(fest_id, user_id, role, added_at) values(?, ?, ?, ?)",
            (fest_id, uid, role, now),
        )

    # 4. One editable game. game_type 'ek' is NOT rating-roster-immutable, so
    #    /state PUT accepts arbitrary JSON and rebroadcasts it verbatim.
    cur.execute(
        "insert into games(fest_id, code, title, game_type, position, scheme_json, state_json, status, "
        "team_list_source, roster_source, revision, created_at, updated_at, slug) "
        "values(?, 'lt', 'Load Test Game', 'ek', 1, '{}', '{}', 'pending', 'fest', 'fest', 1, ?, ?, 'lt')",
        (fest_id, now, now),
    )
    game_id = cur.lastrowid

    con.commit()
    return {
        "stamp": stamp,
        "fest_slug": slug,
        "fest_id": fest_id,
        "game_id": game_id,
        "tokens": tokens,
        "editor_user_ids": user_ids,
    }


def teardown(con: sqlite3.Connection, stamp: str) -> dict:
    slug = f"dope-loadtest-{stamp}"
    cur = con.cursor()
    deleted = {"fest": 0, "events": 0, "users": 0}

    row = cur.execute("select id from fests where slug = ?", (slug,)).fetchone()
    if row:
        fest_id = row[0]
        # events lacks ON DELETE CASCADE in older schemas — delete explicitly first.
        cur.execute("delete from events where fest_id = ?", (fest_id,))
        deleted["events"] = cur.rowcount
        cur.execute("delete from fests where id = ?", (fest_id,))
        deleted["fest"] = cur.rowcount  # cascades games + fest_organizers

    # Test users (sessions cascade on user delete).
    cur.execute("delete from users where username like ?", (f"lt_{stamp}_%",))
    deleted["users"] = cur.rowcount
    con.commit()
    return deleted


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["provision", "teardown"])
    parser.add_argument("--db", default="/var/lib/dope/fest.db", help="path to the live SQLite file")
    parser.add_argument("--editors", type=int, default=3, help="number of editor accounts to create")
    parser.add_argument("--stamp", default=None, help="unique tag; defaults to UTC YYMMDD-HHMM on provision")
    parser.add_argument("--expiry-days", type=int, default=2, help="session lifetime for the test accounts")
    args = parser.parse_args()

    con = connect(args.db)
    try:
        if args.action == "provision":
            stamp = args.stamp or default_stamp()
            result = provision(con, stamp, args.editors, args.expiry_days)
            print(
                f"provisioned fest {result['fest_slug']} (id {result['fest_id']}), "
                f"game {result['game_id']}, {len(result['tokens'])} editors. "
                f"teardown with: --stamp {result['stamp']}",
                file=sys.stderr,
            )
            print(json.dumps(result))
        else:
            if not args.stamp:
                raise SystemExit("teardown requires --stamp")
            result = teardown(con, args.stamp)
            print(json.dumps(result))
    finally:
        con.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
