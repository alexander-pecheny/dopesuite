#!/usr/bin/env python3
"""Resolve parsed-sheet team/player names against the DB and emit an ID-based
apply plan (/tmp/ek_plan.json) for the Go importer.

Reads:
  /tmp/ek_parsed.json  (from parse_sheet.py)
  the fest DB (path arg) -- teams + game rosters for game 8 / fest 6
Emits:
  /tmp/ek_plan.json
and prints every name-resolution decision + any problems.
"""
import json
import sqlite3
import sys

FEST_ID = 6
GAME_ID = 8
# bracket apply order; reseed marker triggers the reseed calc before 1/4.
ORDER = (
    list("ABCDEF") + list("GHIJKL") + list("MNOPQR")
    + ["@reseed:reseed_after_r8"] + list("STUV") + list("WX")
)
# Бой Y (final) intentionally left unplayed (empty in the sheet).


def norm(s):
    return " ".join(str(s).strip().lower().split())


def main():
    db = sys.argv[1]
    parsed = json.load(open("/tmp/ek_parsed.json"))
    con = sqlite3.connect(db)
    con.row_factory = sqlite3.Row

    # team name -> id (fest 6)
    teams = {}
    name_by_id = {}
    for r in con.execute("select id, name from teams where fest_id=?", (FEST_ID,)):
        teams.setdefault(norm(r["name"]), []).append(r["id"])
        name_by_id[r["id"]] = r["name"]

    # rosters: team_id -> list of (player_id, first, last)
    rosters = {}
    for r in con.execute(
        """select gtp.team_id, p.id, p.first_name, p.last_name
           from game_team_players gtp join players p on p.id=gtp.player_id
           where gtp.game_id=?""", (GAME_ID,)):
        rosters.setdefault(r["team_id"], []).append(
            (r["id"], r["first_name"], r["last_name"]))

    problems = []

    def resolve_team(name):
        ids = teams.get(norm(name), [])
        if len(ids) == 1:
            return ids[0]
        problems.append(("TEAM", name, f"matched {len(ids)} teams"))
        return None

    def resolve_player(team_id, cell):
        if not cell:
            return None, "(empty)"
        roster = rosters.get(team_id, [])
        c = norm(cell)
        # 1) exact "first last" or "last first"
        for pid, fn, ln in roster:
            if c in (norm(f"{fn} {ln}"), norm(f"{ln} {fn}")):
                return pid, f"{fn} {ln}"
        # 2) exact last name, or exact first name (unique)
        for key in ("last", "first"):
            hits = [(pid, fn, ln) for pid, fn, ln in roster
                    if c == norm(ln if key == "last" else fn)]
            if len(hits) == 1:
                pid, fn, ln = hits[0]
                return pid, f"{fn} {ln}"
        # 3) substring either way on last name (unique)
        hits = [(pid, fn, ln) for pid, fn, ln in roster
                if norm(ln) and (norm(ln) in c or c in norm(ln))]
        if len(hits) == 1:
            pid, fn, ln = hits[0]
            return pid, f"{fn} {ln}"
        # 4) full-name substring (unique)
        hits = [(pid, fn, ln) for pid, fn, ln in roster
                if c in norm(f"{fn} {ln}") or norm(f"{fn} {ln}") in c]
        if len(hits) == 1:
            pid, fn, ln = hits[0]
            return pid, f"{fn} {ln}"
        problems.append(("PLAYER", f"team={name_by_id.get(team_id,team_id)!r}",
                         f"cell={cell!r}", f"{len(hits)} candidates"))
        return None, "UNRESOLVED"

    # index parsed matches by code
    parsed_matches = {}
    for st in parsed["stages"]:
        for m in st["matches"]:
            parsed_matches[m["code"]] = m

    plan = {"fest_id": FEST_ID, "game_id": GAME_ID, "order": ORDER, "matches": {}}
    unresolved_players = 0
    for code in ORDER:
        if code.startswith("@"):
            continue
        m = parsed_matches[code]
        pteams = []
        print(f"\n=== Бой {code} ===")
        for t in m["teams"]:
            tid = resolve_team(t["name"])
            place = int(t["place"]) if isinstance(t["place"], (int, float)) else None
            themes = []
            pnames = []
            for th in t["themes"]:
                pid, label = resolve_player(tid, th["player"]) if tid else (None, "?")
                if th["player"] and pid is None:
                    unresolved_players += 1
                pnames.append(f"{th['player']}→{label}" if th["player"] else "·")
                themes.append({"theme_index": th["theme_index"],
                               "player_id": pid, "marks": th["marks"]})
            print(f"  {t['name'][:26]:26} id={tid} place={place}  Σ={t['sigma']}")
            pteams.append({"team_id": tid, "place": place, "themes": themes})
        plan["matches"][code] = {"teams": pteams}

    json.dump(plan, open("/tmp/ek_plan.json", "w"), ensure_ascii=False, indent=1)
    print("\n==================== SUMMARY ====================")
    print(f"wrote /tmp/ek_plan.json  matches={len(plan['matches'])}")
    print(f"unresolved player cells: {unresolved_players}")
    print(f"problems: {len(problems)}")
    for p in problems:
        print("  PROBLEM", p)


if __name__ == "__main__":
    main()
