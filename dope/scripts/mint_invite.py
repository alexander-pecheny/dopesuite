#!/usr/bin/env python3
"""Mint a one-shot invite code for /register testing.

Usage: DOPE_DB=fest.db uv run python scripts/mint_invite.py [days]

Inserts an invites row created by the system user, expiring `days` days from
now (default 7). Prints the code to stdout — paste it into the /register form.
"""

import base64
import datetime as dt
import os
import secrets
import sqlite3
import sys

# Matches the server's newInviteCode.
INVITE_CODE_BYTES = 12
DEFAULT_LIFETIME_DAYS = 7
# How many random codes to try before giving up on a free one.
CODE_ATTEMPTS = 5


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def utc_in(days: int) -> str:
    return (dt.datetime.now(dt.timezone.utc) + dt.timedelta(days=days)).strftime("%Y-%m-%dT%H:%M:%SZ")


def new_code() -> str:
    # Match db.go newInviteCode: 12 random bytes, RFC 4648 base32, uppercase, strip padding.
    return base64.b32encode(secrets.token_bytes(INVITE_CODE_BYTES)).decode("ascii").rstrip("=").upper()


def ensure_system_user(cur) -> int:
    """The system user's id. It is created lazily on first import, so bootstrap
    one if there is none yet."""
    row = cur.execute("select id from users where is_system = 1 limit 1").fetchone()
    if row is not None:
        return row[0]
    now = utc_now()
    cur.execute(
        "insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at) "
        "values(null, null, 'system', 1, ?, ?)",
        (now, now),
    )
    return cur.lastrowid


def main() -> int:
    db_path = os.environ.get("DOPE_DB", "fest.db")
    if not os.path.exists(db_path):
        sys.stderr.write(f"db not found: {db_path}\n")
        return 1

    days = DEFAULT_LIFETIME_DAYS
    if len(sys.argv) > 1:
        days = int(sys.argv[1])

    con = sqlite3.connect(db_path)
    con.execute("PRAGMA foreign_keys = ON")
    cur = con.cursor()

    system_id = ensure_system_user(cur)

    for _ in range(CODE_ATTEMPTS):
        code = new_code()
        try:
            cur.execute(
                "insert into invites(code, created_by, created_at, expires_at) values(?, ?, ?, ?)",
                (code, system_id, utc_now(), utc_in(days)),
            )
            con.commit()
            print(code)
            return 0
        except sqlite3.IntegrityError:
            continue

    sys.stderr.write("could not allocate invite code\n")
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
