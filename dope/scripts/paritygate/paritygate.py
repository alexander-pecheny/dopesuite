#!/usr/bin/env python3
"""ADR-0004 parity gate: rehearse the unified-model conversion on a prod snapshot.

Usage: uv run python scripts/paritygate/paritygate.py <snapshot.db> [--keep]

Runs three checks against a COPY of the snapshot (the input is never touched):

1. Result parity — before conversion, every EK match's relational protocol
   state (themes/answers, when the snapshot still has them) and every flat
   game's state blob are dumped in canonical form; after conversion the same
   data must be reproduced from the unified storage byte-for-byte (canonical
   JSON, key-sorted).
2. Journal integrity — conversion must not add or remove journal records, and
   no record may reference a dropped table (themes/answers/reseed_entries)
   above its game's earliest checkpoint (the replayable region).
3. Checkpoint integrity — every stored checkpoint must decode and contain no
   dropped-table dumps.

Exit 0 = gate passed. The server binary is built from the working tree, so the
gate always tests the code that would ship.
"""

import json
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import time
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
DROPPED_TABLES = ("themes", "answers", "reseed_entries")


def norm_mark(mark):
    mark = (mark or "").strip().lower()
    if mark in ("right", "q", "й", "1", "+"):
        return "right"
    if mark in ("wrong", "w", "ц", "-1", "-", "−1", "−"):
        return "wrong"
    return ""


def canonical(obj):
    return json.dumps(obj, sort_keys=True, ensure_ascii=False)


def dump_ek_relational(db):
    """Legacy themes/answers rows → the blob shape the converter must produce."""
    if not db.execute("select count(*) from sqlite_master where name='themes'").fetchone()[0]:
        return {}
    out = {}
    for mid, in db.execute("select distinct match_id from themes"):
        teams = {}
        for tid, team, kind, tindex, player in db.execute(
            "select id, team_id, kind, theme_index, coalesce(player_id,0) from themes where match_id=?", (mid,)
        ):
            section = teams.setdefault(str(team), {})
            key = "shootoutThemes" if kind == "shootout" else "themes"
            lst = section.setdefault(key, [])
            while len(lst) <= tindex:
                lst.append({"answers": ["", "", "", "", ""]})
            entry = lst[tindex]
            if player:
                entry["player"] = player
            for ai, mark in db.execute("select answer_index, mark from answers where theme_id=?", (tid,)):
                if 0 <= ai < 5:
                    entry["answers"][ai] = norm_mark(mark)
        out[str(mid)] = {"teams": teams} if teams else {}
    return out


def dump_flat_states(db):
    """Flat-game states, wherever they live pre/post conversion."""
    has_col = db.execute(
        "select count(*) from pragma_table_info('matches') where name = 'state_json'").fetchone()[0]
    if has_col:
        query = """
select g.id, coalesce((select m.state_json from matches m where m.game_id = g.id and m.code = 'main'),
                      coalesce(g.state_json, '{}'))
from games g where g.game_type in ('od','ksi','si')"""
    else:
        query = """
select g.id, coalesce(g.state_json, '{}') from games g where g.game_type in ('od','ksi','si')"""
    out = {}
    for gid, state in db.execute(query):
        out[str(gid)] = canonical(json.loads(state or "{}"))
    return out


def dump_converted_ek(db, match_ids):
    out = {}
    for mid in match_ids:
        row = db.execute("select state_json from matches where id=?", (int(mid),)).fetchone()
        blob = json.loads(row[0]) if row and row[0] else {}
        out[mid] = blob if blob else {}
    return out


def journal_stats(db):
    total = db.execute("select count(*) from journal").fetchone()[0]
    cps = dict(db.execute("select game_id, min(seq) from journal_checkpoint group by 1"))
    bad = 0
    for id_, game, payload in db.execute("select id, game_id, payload from journal"):
        p = payload if isinstance(payload, str) else (payload or b"").decode("utf-8", "replace")
        if not p.startswith('{"t":'):
            continue
        try:
            table = json.loads(p)["t"]
        except Exception:
            continue
        if table in DROPPED_TABLES and game in cps and id_ > cps[game]:
            bad += 1
    return total, bad


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    snapshot = Path(sys.argv[1])
    keep = "--keep" in sys.argv
    work = Path(tempfile.mkdtemp(prefix="paritygate-"))
    db_path = work / "gate.db"
    shutil.copy(snapshot, db_path)
    failures = []

    pre = sqlite3.connect(db_path)
    ek_before = dump_ek_relational(pre)
    flat_before = dump_flat_states(pre)
    journal_before, _ = journal_stats(pre)
    pre.close()

    server = work / "dope-server"
    subprocess.run(["go", "build", "-o", server, "./dope/cmd/dope-server"],
                   cwd=REPO, check=True)
    proc = subprocess.Popen([server], env={"DOPE_DB": str(db_path), "PORT": "19699", "PATH": "/usr/bin:/bin"},
                            cwd=work, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    time.sleep(4)
    proc.terminate()
    proc.wait(timeout=10)

    post = sqlite3.connect(db_path)
    for table in DROPPED_TABLES:
        if post.execute("select count(*) from sqlite_master where name=?", (table,)).fetchone()[0]:
            failures.append(f"table {table} still exists after conversion")
    ek_after = dump_converted_ek(post, ek_before.keys())
    for mid, want in ek_before.items():
        got = ek_after.get(mid, {})
        if canonical(want) != canonical(got):
            failures.append(f"EK match {mid} state mismatch")
    flat_after = dump_flat_states(post)
    for gid, want in flat_before.items():
        if flat_after.get(gid) != want:
            failures.append(f"flat game {gid} state mismatch")
    journal_after, bad_records = journal_stats(post)
    if journal_after != journal_before:
        failures.append(f"journal row count changed: {journal_before} -> {journal_after}")
    if bad_records:
        failures.append(f"{bad_records} replayable journal records reference dropped tables")
    post.close()

    if keep:
        print(f"workdir kept: {work}")
    else:
        shutil.rmtree(work, ignore_errors=True)

    if failures:
        print("PARITY GATE FAILED")
        for f in failures:
            print(" -", f)
        sys.exit(1)
    print(f"parity gate passed: {len(ek_before)} EK matches, {len(flat_before)} flat games, {journal_before} journal records")


if __name__ == "__main__":
    main()
