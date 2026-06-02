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
import socket
import sqlite3
import sys
import threading
import time
import urllib.error
import urllib.request


def install_dns_cache() -> None:
    """Memoize socket.getaddrinfo so each host resolves exactly once.

    urllib does no DNS caching, so a fleet of hundreds/thousands of viewer
    threads (plus the editor) each fire a fresh getaddrinfo on every request.
    That thundering herd overwhelms the local resolver (e.g. Tailscale MagicDNS
    on 100.100.100.100), which then returns EAI_NONAME spuriously — surfaced as
    "[Errno 8] nodename nor servname provided, or not known". Resolving each
    (host, port, ...) once and sharing the result removes the herd entirely.
    """
    real = socket.getaddrinfo
    cache: dict = {}
    lock = threading.Lock()

    def cached_getaddrinfo(*args, **kwargs):
        key = (args, tuple(sorted(kwargs.items())))
        with lock:
            hit = cache.get(key)
        if hit is not None:
            return hit
        res = real(*args, **kwargs)
        with lock:
            cache[key] = res
        return res

    socket.getaddrinfo = cached_getaddrinfo


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
        # Capture the high-water audit_log id so teardown can purge exactly the
        # rows this demo's edits generated (and then VACUUM to reclaim space).
        "max_audit_id": (con.execute("select coalesce(max(id),0) m from audit_log").fetchone()["m"]),
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

    # Purge the audit_log rows this demo's edits generated, then reclaim the
    # space. Scoped to rows newer than the setup high-water mark, so we never
    # touch pre-existing history. (Older backups lack the key — skip then.)
    purged_audit = 0
    if "max_audit_id" in snap:
        purged_audit = cur.execute("delete from audit_log where id>?", (snap["max_audit_id"],)).rowcount
    con.commit()
    if "max_audit_id" in snap:
        con.isolation_level = None  # VACUUM cannot run inside a transaction
        con.execute("VACUUM")
    os.remove(path)
    return {"restored_games": len(snap["games"]), "restored_slots": len(snap["match_slots"]),
            "restored_results": len(snap["match_results"]), "removed_user": snap["temp_user_id"],
            "purged_audit_rows": purged_audit}


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

    def patch_json(self, path: str, obj) -> None:
        self._req("PATCH", path, json.dumps(obj).encode())


def pct(vals: list, p: float) -> float:
    if not vals:
        return 0.0
    s = sorted(vals)
    return s[int(p * (len(s) - 1))]


class Stats:
    """Thread-safe collector shared by the editor loop and the viewer pool.

    edit_ms   — editor request round-trip (write path under the global mutex)
    view_ms   — edit->viewer propagation: a viewer reads `_lt_ts` (a monotonic
                send-stamp the editor embeds in OD/KSI state) back out of the
                SSE broadcast. Editor and viewers share this process's clock,
                so the delta needs no clock sync. EK match edits don't carry
                the stamp, so view_ms reflects OD/KSI only.
    """

    def __init__(self):
        self._lock = threading.Lock()
        self.edit_ms: list = []
        self.view_ms: list = []
        self.edit_ok = 0
        self.edit_err = 0
        self.events = 0
        self.viewer_fail = 0
        self.viewer_drop = 0
        self.peak_viewers = 0

    def add_edit(self, ms: float, ok: bool):
        with self._lock:
            self.edit_ms.append(ms)
            if ok:
                self.edit_ok += 1
            else:
                self.edit_err += 1

    def add_event(self, view_ms: float | None):
        with self._lock:
            self.events += 1
            if view_ms is not None:
                self.view_ms.append(view_ms)

    def bump(self, field: str, n: int = 1):
        with self._lock:
            setattr(self, field, getattr(self, field) + n)

    def note_peak(self, n: int):
        with self._lock:
            if n > self.peak_viewers:
                self.peak_viewers = n


class Viewer(threading.Thread):
    """One long-lived SSE reader, mirroring a browser on a viewer page.

    /events is public (no auth), so this needs no session — exactly what a real
    spectator's connection looks like through nginx.
    """

    def __init__(self, base: str, fest: int, stats: Stats):
        super().__init__(daemon=True)
        self.url = f"{base.rstrip('/')}/events?fest_id={fest}"
        self.stats = stats
        self.stop = threading.Event()
        self._resp = None

    def shutdown(self):
        self.stop.set()
        try:
            if self._resp is not None:
                self._resp.close()
        except Exception:  # noqa: BLE001
            pass

    def run(self):
        req = urllib.request.Request(self.url)
        req.add_header("Accept", "text/event-stream")
        try:
            self._resp = urllib.request.urlopen(req, timeout=15)
        except Exception:  # noqa: BLE001
            self.stats.bump("viewer_fail")
            return
        if getattr(self._resp, "status", 200) != 200:
            self.stats.bump("viewer_fail")
            return
        buf = []
        while not self.stop.is_set():
            try:
                raw = self._resp.readline()
            except (socket.timeout, TimeoutError):
                continue
            except Exception:  # noqa: BLE001 — connection dropped under load
                if not self.stop.is_set():
                    self.stats.bump("viewer_drop")
                return
            if not raw:  # server closed the stream
                if not self.stop.is_set():
                    self.stats.bump("viewer_drop")
                return
            line = raw.decode("utf-8", "replace").rstrip("\r\n")
            if line == "":
                if buf:
                    self._on_frame("".join(buf))
                    buf = []
            elif line.startswith("data:"):
                buf.append(line[len("data:"):].strip())

    def _on_frame(self, data: str):
        view_ms = None
        try:
            env = json.loads(data)
            inner = env.get("data") if isinstance(env, dict) else None
            if isinstance(inner, dict) and "_lt_ts" in inner:
                view_ms = (time.monotonic() - float(inner["_lt_ts"])) * 1000
        except Exception:  # noqa: BLE001 — keepalives / non-JSON frames
            pass
        self.stats.add_event(view_ms)


def run_viewer_pool(base: str, fest: int, stats: Stats, vmin: int, vmax: int,
                    period: float, duration: float, stop_all: threading.Event):
    """Hold a fleet of SSE viewers that continually ramps between vmin and vmax.

    The target count traces a triangle wave (vmin -> vmax -> vmin every
    `period` seconds), so the server sees fan-out fan in and out the whole run.
    """
    viewers: list[Viewer] = []
    start = time.monotonic()
    while not stop_all.is_set() and time.monotonic() - start < duration:
        viewers = [v for v in viewers if v.is_alive()]  # drop dead/failed
        t = time.monotonic() - start
        if vmax <= vmin or period <= 0:
            target = vmin
        else:
            phase = (t % period) / period
            tri = 1 - abs(2 * phase - 1)  # 0 -> 1 -> 0
            target = round(vmin + (vmax - vmin) * tri)
        while len(viewers) < target:
            v = Viewer(base, fest, stats)
            v.start()
            viewers.append(v)
        while len(viewers) > target:
            viewers.pop().shutdown()
        stats.note_peak(len(viewers))
        time.sleep(1)
    for v in viewers:
        v.shutdown()
    for v in viewers:
        v.join(timeout=3)


def print_report(stats: Stats, duration: float, vmin: int, vmax: int):
    em, vm = stats.edit_ms, stats.view_ms
    total_edits = stats.edit_ok + stats.edit_err
    eps = total_edits / duration if duration else 0
    print("\n================ realtime demo report ================", flush=True)
    print(f"  duration {duration:.0f}s   viewers {vmin}-{vmax} (peak {stats.peak_viewers})", flush=True)
    print(f"  edits: {total_edits} total  {stats.edit_ok} ok  {stats.edit_err} err  ({eps:.1f}/s)", flush=True)
    print(f"  SSE events received {stats.events}  (viewer connect fails {stats.viewer_fail}, drops {stats.viewer_drop})", flush=True)
    print(f"\n  {'metric':<22}{'p50':>9}{'p95':>9}{'p99':>9}{'max':>9}{'n':>8}", flush=True)
    print(f"  {'-' * 64}", flush=True)
    print(f"  {'edit latency ms':<22}{pct(em,.5):>9.0f}{pct(em,.95):>9.0f}{pct(em,.99):>9.0f}{(max(em) if em else 0):>9.0f}{len(em):>8}", flush=True)
    print(f"  {'view latency ms':<22}{pct(vm,.5):>9.0f}{pct(vm,.95):>9.0f}{pct(vm,.99):>9.0f}{(max(vm) if vm else 0):>9.0f}{len(vm):>8}", flush=True)
    print("======================================================", flush=True)
    print("  edit = editor PUT/POST round-trip; view = edit->viewer SSE propagation (OD/KSI).", flush=True)


def simulate(base: str, fest: int, od: int, ksi: int, ek: int, ek_match: str,
             token: str, duration: float, eps: float,
             burst: int, burst_teams: int,
             viewers_min: int, viewers_max: int, ramp_period: float) -> None:
    install_dns_cache()  # keep the viewer fleet from flooding the resolver
    rng = random.Random()
    c = Client(base, token)
    stats = Stats()
    interval = (1.0 / eps) if eps > 0 else 0.3

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
          f"EK match {ek_match}({n_ek_teams}t x{n_themes}th x{n_answers}a) for {duration:.0f}s "
          f"@ {eps:.1f} edits/s, viewers {viewers_min}-{viewers_max}", flush=True)

    # Hold a continually-ramping fleet of SSE viewers for the whole run.
    stop_all = threading.Event()
    pool = threading.Thread(
        target=run_viewer_pool,
        args=(base, fest, stats, viewers_min, viewers_max, ramp_period, duration, stop_all),
        daemon=True,
    )
    pool.start()

    # EK marks can only be edited on an active match; reopen it for the demo.
    # teardown restores its finished status from the snapshot.
    try:
        c.post_json(f"/api/fest/{fest}/games/{ek}/matches/{ek_match}/update", {"finished": False})
    except Exception as e:  # noqa: BLE001
        print(f"  warning: could not reopen EK match {ek_match}: {e}", flush=True)

    def timed(fn) -> bool:
        """Run one HTTP edit, record its latency, return True on success."""
        t0 = time.monotonic()
        try:
            fn()
            stats.add_edit((time.monotonic() - t0) * 1000, ok=True)
            return True
        except urllib.error.HTTPError as e:
            stats.add_edit((time.monotonic() - t0) * 1000, ok=False)
            print(f"\n  edit error: {e.code} {e.reason}", flush=True)
            return False
        except Exception as e:  # noqa: BLE001 — surface anything else but keep going
            stats.add_edit((time.monotonic() - t0) * 1000, ok=False)
            print(f"\n  edit error: {e}", flush=True)
            return False

    # OD entries hold TEAM NUMBERS, not scores: entries[question][slot] is the
    # number of a team that "took" that question. A team's total is simply how
    # many completed questions it appears in (sumRow/teamTookQuestion), and a
    # number may appear at most once per question (a repeat is flagged as a
    # duplicate). So we assign DISTINCT real team numbers, with a varying random
    # subset of teams taking each question — that makes the Итог standings
    # re-sort between ticks without ever producing duplicates.
    od_numbers = []
    for t in od_state.get("teams", []):
        num = t.get("number") if isinstance(t, dict) else t
        if isinstance(num, int) and num > 0:
            od_numbers.append(num)
    # Concentrate KSI edits on the first handful of participants so changes land
    # where a viewer is looking, in a burst per tick.
    ksi_parts = min(burst_teams, n_participants)
    rounds = min(24, n_entries)
    deadline = time.monotonic() + duration
    edits = {"od": 0, "ksi": 0, "ek": 0}
    nxt = 0
    while time.monotonic() < deadline:
        which = ("od", "ksi", "ek")[nxt % 3]
        nxt += 1
        if which == "od" and n_entries and od_numbers:
            # Each tick, re-roll which distinct teams "took" each (completed)
            # question. Totals = #questions-taken, so varying membership makes
            # the standings re-sort; distinct numbers per column means no
            # duplicate flags. Rounds must be complete — Итог only aggregates
            # completed questions.
            for r in range(rounds):
                od_state["completed"][r] = True
                row = od_state["entries"][r]
                present = [n for n in od_numbers if rng.random() < 0.6]
                rng.shuffle(present)
                for i in range(len(row)):
                    row[i] = present[i] if i < len(present) else 0
            od_state["_lt_ts"] = time.monotonic()  # propagation stamp (echoed in broadcast)
            if timed(lambda: c.put_json(f"/api/fest/{fest}/games/{od}/state", od_state)):
                edits["od"] += 1
        elif which == "ksi" and n_ksi_themes and n_participants:
            for _ in range(burst):
                p = rng.randrange(ksi_parts)
                q = rng.randrange(n_ksi_answers)
                cur = ksi_state["themes"][0]["answers"][p][q]
                ksi_state["themes"][0]["answers"][p][q] = "wrong" if cur == "right" else "right"
            ksi_state["_lt_ts"] = time.monotonic()
            if timed(lambda: c.put_json(f"/api/fest/{fest}/games/{ksi}/state", ksi_state)):
                edits["ksi"] += 1
        else:
            ok_any = False
            for _ in range(max(1, burst // 2)):
                ok_any = timed(lambda: c.post_json(
                    f"/api/fest/{fest}/games/{ek}/matches/{ek_match}/update", {
                        "team": rng.randrange(n_ek_teams),
                        "theme": rng.randrange(min(6, n_themes)),
                        "answer": rng.randrange(n_answers),
                        "mark": rng.choice(["right", "wrong"]),
                    })) or ok_any
            if ok_any:
                edits["ek"] += 1
        print(f"\r  edits od={edits['od']} ksi={edits['ksi']} ek={edits['ek']} "
              f"err={stats.edit_err} viewers={stats.peak_viewers} events={stats.events}   ",
              end="", flush=True)
        time.sleep(interval)

    stop_all.set()
    pool.join(timeout=10)
    print_report(stats, duration, viewers_min, viewers_max)


# --------------------------------------------------- simulate (realistic) ---

def simulate_realistic(base: str, fest: int, game_id: int, game_type: str,
                       token: str, duration: float, eps: float, editors: int,
                       viewers_min: int, viewers_max: int, ramp_period: float) -> None:
    """Model the REAL production workload: one active game, a handful of
    concurrent organizers each toggling a single cell via the same scoped PATCH
    the UI sends, with a fleet of SSE spectators watching.

    Unlike `simulate` (full-state PUT, one serial editor), this exercises the
    `patchGameState` path and keeps the game's real state blob intact, so the
    server's per-edit cost — re-marshal + write the whole state_json, audit it
    twice, and fan the whole next state out to every viewer — is realistic and
    scales with the blob size (OD is the worst case).

    Each edit carries a single real-cell op plus an `_lt_ts` op so viewers can
    still compute propagation latency out of the broadcast state.
    """
    install_dns_cache()  # keep the viewer fleet from flooding the resolver
    c = Client(base, token)
    stats = Stats()

    state = c.get_json(f"/api/fest/{fest}/games/{game_id}/state")
    if game_type == "od":
        entries = state.get("entries") or []
        n_entries = len(entries)
        n_slots = len(entries[0]) if n_entries else 0
        od_numbers = []
        for t in state.get("teams", []):
            num = t.get("number") if isinstance(t, dict) else t
            if isinstance(num, int) and num > 0:
                od_numbers.append(num)
        if not (n_entries and n_slots and od_numbers):
            raise SystemExit("OD game has no editable entries/teams to patch")

        def make_ops(rng, ts):
            return [
                {"op": "set",
                 "path": ["entries", rng.randrange(n_entries), rng.randrange(n_slots)],
                 "value": rng.choice(od_numbers)},
                {"op": "set", "path": ["_lt_ts"], "value": ts},
            ]
        dims = f"OD({n_entries}x{n_slots}, {len(od_numbers)} teams)"
    elif game_type == "ksi":
        themes = state.get("themes") or []
        answers = themes[0]["answers"] if themes else []
        n_parts = len(answers)
        n_ans = len(answers[0]) if n_parts else 0
        if not (n_parts and n_ans):
            raise SystemExit("KSI game has no editable theme-0 answers to patch")

        def make_ops(rng, ts):
            return [
                {"op": "set",
                 "path": ["themes", 0, "answers", rng.randrange(n_parts), rng.randrange(n_ans)],
                 "value": rng.choice(["right", "wrong"])},
                {"op": "set", "path": ["_lt_ts"], "value": ts},
            ]
        dims = f"KSI(theme0 {n_parts}p x {n_ans}a)"
    else:
        raise SystemExit(f"realistic mode supports od/ksi, not {game_type!r}")

    path = f"/api/fest/{fest}/games/{game_id}/state"
    print(f"simulating REALISTIC: {dims} via single-cell PATCH, {editors} editors "
          f"@ {eps:.1f} edits/s total, viewers {viewers_min}-{viewers_max} for {duration:.0f}s",
          flush=True)

    stop_all = threading.Event()
    pool = threading.Thread(
        target=run_viewer_pool,
        args=(base, fest, stats, viewers_min, viewers_max, ramp_period, duration, stop_all),
        daemon=True,
    )
    pool.start()

    # Closed-loop per editor: each waits for its PATCH to land, then paces so the
    # fleet aims for `eps` total. If the server can't keep up the editors fall
    # behind and achieved eps drops — exactly how a real organizer experiences it.
    per_editor_interval = (editors / eps) if eps > 0 else 0.3
    deadline = time.monotonic() + duration

    def editor_loop(editor_id: int):
        rng = random.Random(editor_id + 1)
        while time.monotonic() < deadline and not stop_all.is_set():
            ts = time.monotonic()
            ops = make_ops(rng, ts)
            t0 = time.monotonic()
            try:
                c.patch_json(path, {"ops": ops})
                stats.add_edit((time.monotonic() - t0) * 1000, ok=True)
            except urllib.error.HTTPError as e:
                stats.add_edit((time.monotonic() - t0) * 1000, ok=False)
                print(f"\n  edit error: {e.code} {e.reason}", flush=True)
            except Exception as e:  # noqa: BLE001 — surface but keep going
                stats.add_edit((time.monotonic() - t0) * 1000, ok=False)
                print(f"\n  edit error: {e}", flush=True)
            slack = per_editor_interval - (time.monotonic() - ts)
            if slack > 0:
                time.sleep(slack)

    threads = [threading.Thread(target=editor_loop, args=(i,), daemon=True) for i in range(editors)]
    for t in threads:
        t.start()

    while time.monotonic() < deadline:
        time.sleep(1)
        total = stats.edit_ok + stats.edit_err
        print(f"\r  edits={total} ok={stats.edit_ok} err={stats.edit_err} "
              f"viewers={stats.peak_viewers} events={stats.events}   ", end="", flush=True)

    stop_all.set()
    for t in threads:
        t.join(timeout=5)
    pool.join(timeout=10)
    print_report(stats, duration, viewers_min, viewers_max)


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
    sim.add_argument("--eps", type=float, default=3.0, help="edits per second (one game edit per tick, rotating od/ksi/ek)")
    sim.add_argument("--burst", type=int, default=8, help="cell changes per tick (visible movement)")
    sim.add_argument("--burst-teams", type=int, default=20, help="restrict edits to the first N teams/participants")
    sim.add_argument("--viewers-min", type=int, default=0, help="min concurrent SSE viewers in the ramp")
    sim.add_argument("--viewers-max", type=int, default=0, help="max concurrent SSE viewers in the ramp")
    sim.add_argument("--ramp-period", type=float, default=60.0, help="seconds for one min->max->min viewer cycle")
    sim.add_argument("--mode", choices=["visual", "patch"], default="visual",
                     help="visual = full-state PUT across od/ksi/ek (watchable demo); "
                          "patch = realistic single-cell PATCH load on one game")
    sim.add_argument("--editors", type=int, default=6, help="concurrent editors (patch mode)")
    sim.add_argument("--game", choices=["od", "ksi"], default="od", help="active game to edit (patch mode)")

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
    elif args.mode == "patch":
        game_id = {"od": args.od, "ksi": args.ksi}[args.game]
        simulate_realistic(args.base, args.fest, game_id, args.game, args.token,
                           args.duration, args.eps, args.editors,
                           args.viewers_min, args.viewers_max, args.ramp_period)
    else:
        simulate(args.base, args.fest, args.od, args.ksi, args.ek, args.ek_match,
                 args.token, args.duration, args.eps, args.burst, args.burst_teams,
                 args.viewers_min, args.viewers_max, args.ramp_period)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
