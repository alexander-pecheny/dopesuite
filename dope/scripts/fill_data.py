#!/usr/bin/env python3
"""Fill a fest's game with random answers for end-to-end testing.

Usage:
    uv run python scripts/fill_data.py --db tournament.db --fest <slug> --game <od|ksi|ek> [--stage <code>]

Per-cell randomization:
  - od (chgk-style):    "right" or blank ("")     i.e. 1 or 0
  - ksi / ek:           "right" or "wrong"        i.e. + or -

When --game ek, --stage is required so you can fill one stage at a time and
verify propagation into later stages between runs.

After writing match-table answers the script also refreshes match_results
(total, plus, metrics_json) so the UI standings match. For ek it additionally
writes match_results.place by rank-of-total — mimicking the host clicking
1/2/3/4 on the winner — which is what downstream from_match slots resolve
against. For od/ksi place stays 0 so the UI keeps auto-ranking by total.

KSI games that use JSON state instead of match tables are filled directly in
games.state_json.
"""

import argparse
import datetime as dt
import json
import os
import random
import sqlite3
import sys

QUESTION_VALUES = (10, 20, 30, 40, 50)


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def pick_mark(game_type: str) -> str:
    if game_type == "od":
        return "right" if random.random() < 0.5 else ""
    return "right" if random.random() < 0.5 else "wrong"


def metrics_json(correct, wrong) -> str:
    metrics = {"correctCounts": list(correct), "wrongCounts": list(wrong)}
    for i, value in enumerate(QUESTION_VALUES):
        metrics[f"correct_{value}"] = correct[i]
        metrics[f"wrong_{value}"] = wrong[i]
    return json.dumps(metrics)


def bump_fest_revision(cur, fest_id, event_type, payload):
    now = utc_now()
    cur.execute("update fests set revision = revision + 1, updated_at = ? where id = ?", (now, fest_id))
    revision = cur.execute("select revision from fests where id = ?", (fest_id,)).fetchone()[0]
    cur.execute(
        "insert into events(fest_id, revision, type, payload_json, created_at) "
        "values(?, ?, ?, ?, ?)",
        (fest_id, revision, event_type, payload, now),
    )


def fill_ksi_json_state(cur, fest_id, game_id):
    row = cur.execute(
        "select scheme_json, state_json from games where fest_id = ? and id = ?",
        (fest_id, game_id),
    ).fetchone()
    if row is None:
        return None

    scheme_raw, state_raw = row
    scheme = json.loads(scheme_raw or "{}")
    state = json.loads(state_raw or "{}")
    if not isinstance(state, dict):
        state = {}

    participants = state.get("participants")
    if not isinstance(participants, list) or not participants:
        participants = scheme.get("participants")
    if not isinstance(participants, list) or not participants:
        return None

    raw_themes = state.get("themes")
    if not isinstance(raw_themes, list):
        raw_themes = []
    themes_count = len(raw_themes)
    if themes_count == 0:
        themes_count = int(scheme.get("themes") or 0)
    if themes_count <= 0:
        return None

    themes = []
    filled = 0
    for theme_index in range(themes_count):
        theme = raw_themes[theme_index] if theme_index < len(raw_themes) else {}
        if not isinstance(theme, dict):
            theme = {}
        next_theme = dict(theme)
        answers = []
        for _ in participants:
            row_answers = []
            for _ in QUESTION_VALUES:
                row_answers.append(pick_mark("ksi"))
                filled += 1
            answers.append(row_answers)
        next_theme["answers"] = answers
        themes.append(next_theme)

    state["participants"] = participants
    state["themes"] = themes
    if not isinstance(state.get("finished"), bool):
        state["finished"] = False

    payload = json.dumps(state, ensure_ascii=False, separators=(",", ":"))
    cur.execute(
        "update games set state_json = ?, updated_at = ? where fest_id = ? and id = ?",
        (payload, utc_now(), fest_id, game_id),
    )
    bump_fest_revision(cur, fest_id, "game:state", payload)
    return {
        "participants": len(participants),
        "themes": themes_count,
        "answers": len(QUESTION_VALUES),
        "filled": filled,
    }


def fill_match(cur, match_id, game_type):
    rows = cur.execute(
        "select id, team_id from themes where match_id = ? and kind = 'regular' "
        "order by team_id, theme_index",
        (match_id,),
    ).fetchall()
    if not rows:
        return {}

    per_team = {}
    for theme_id, team_id in rows:
        stats = per_team.setdefault(team_id, {
            "total": 0, "plus": 0,
            "correct": [0] * 5, "wrong": [0] * 5,
        })
        for answer_index in range(5):
            mark = pick_mark(game_type)
            cur.execute(
                "insert into answers(theme_id, answer_index, mark) values(?, ?, ?) "
                "on conflict(theme_id, answer_index) do update set mark = excluded.mark",
                (theme_id, answer_index, mark),
            )
            value = QUESTION_VALUES[answer_index]
            if mark == "right":
                stats["total"] += value
                stats["plus"] += value
                stats["correct"][answer_index] += 1
            elif mark == "wrong":
                stats["total"] -= value
                stats["wrong"][answer_index] += 1

    places = {}
    if game_type == "ek":
        ordered = sorted(per_team.items(), key=lambda kv: (-kv[1]["total"], kv[0]))
        places = {tid: float(rank + 1) for rank, (tid, _) in enumerate(ordered)}

    for team_id, stats in per_team.items():
        cur.execute(
            "insert into match_results(match_id, team_id, place, total, plus, tiebreak, metrics_json) "
            "values(?, ?, ?, ?, ?, 0, ?) "
            "on conflict(match_id, team_id) do update set "
            "place = excluded.place, total = excluded.total, plus = excluded.plus, "
            "tiebreak = excluded.tiebreak, metrics_json = excluded.metrics_json",
            (
                match_id,
                team_id,
                places.get(team_id, 0.0),
                stats["total"],
                stats["plus"],
                metrics_json(stats["correct"], stats["wrong"]),
            ),
        )

    return per_team


def bump_match_revision(cur, fest_id, match_id, code):
    cur.execute("update matches set revision = revision + 1 where id = ?", (match_id,))
    bump_fest_revision(cur, fest_id, "match:update", json.dumps({"code": code, "source": "fill_data.py"}))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--db", required=True, help="path to sqlite database")
    parser.add_argument("--fest", required=True, help="fest slug")
    parser.add_argument("--game", required=True, choices=("od", "ksi", "ek"), help="game type")
    parser.add_argument("--stage", help="stage code (required when --game ek)")
    parser.add_argument("--seed", type=int, default=42, help="random seed for reproducibility (default: 42)")
    args = parser.parse_args()

    if args.game == "ek" and not args.stage:
        parser.error("--stage is required when --game ek")
    if args.game != "ek" and args.stage:
        sys.stderr.write("warning: --stage is ignored for non-ek games\n")
        args.stage = None

    random.seed(args.seed)

    if not os.path.exists(args.db):
        sys.stderr.write(f"db not found: {args.db}\n")
        return 1

    con = sqlite3.connect(args.db)
    con.execute("PRAGMA foreign_keys = ON")
    cur = con.cursor()

    row = cur.execute("select id from fests where slug = ?", (args.fest,)).fetchone()
    if row is None:
        sys.stderr.write(f"fest not found: slug={args.fest}\n")
        return 1
    fest_id = row[0]

    row = cur.execute(
        "select id from games where fest_id = ? and game_type = ? order by position, id limit 1",
        (fest_id, args.game),
    ).fetchone()
    if row is None:
        sys.stderr.write(f"no {args.game} game in fest {args.fest}\n")
        return 1
    game_id = row[0]

    if args.game == "ksi":
        summary = fill_ksi_json_state(cur, fest_id, game_id)
        if summary is not None:
            con.commit()
            print(
                "filled ksi state: "
                f"{summary['participants']} participants × "
                f"{summary['themes']} themes × "
                f"{summary['answers']} answers = {summary['filled']} cells"
            )
            return 0

    where = "fest_id = ? and game_id = ?"
    params = [fest_id, game_id]
    if args.stage:
        srow = cur.execute(
            "select id from stages where game_id = ? and code = ?",
            (game_id, args.stage),
        ).fetchone()
        if srow is None:
            sys.stderr.write(f"stage not found: code={args.stage}\n")
            return 1
        where += " and stage_id = ?"
        params.append(srow[0])

    matches = cur.execute(
        f"select id, code, title from matches where {where} order by position, id",
        params,
    ).fetchall()
    if not matches:
        sys.stderr.write("no matches in scope\n")
        return 1

    filled = skipped = 0
    for match_id, code, title in matches:
        stats = fill_match(cur, match_id, args.game)
        if not stats:
            skipped += 1
            sys.stderr.write(f"skip {code} ({title!r}): no themes — slots not yet resolved to teams\n")
            continue
        bump_match_revision(cur, fest_id, match_id, code)
        filled += 1
        print(f"filled {code} ({title!r}): {len(stats)} teams")

    con.commit()
    print(f"done. filled={filled} skipped={skipped}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
