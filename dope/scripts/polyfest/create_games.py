"""Create Polyfest 2026's six games in a dope fest through dope-cli.

Usage: uv run python create_games.py dope-cli FEST DSLDIR [slug ...]

The ОД goes first: КИнСбФ seeds from its code, od-1. Name slugs to create
only those games.
"""
import json, subprocess, sys
cli, fest, d = sys.argv[1:4]
def api(method, path, body):
    out = subprocess.run([cli, "api", method, path, json.dumps(body)], capture_output=True, text=True)
    if out.returncode:
        sys.exit(f"{method} {path}: {out.stdout}{out.stderr}")
    return json.loads(out.stdout)
def dsl(name):
    return open(f"{d}/{name}.dsl").read()
games = [
    ("od", "ОД", {"game_type": "od", "od_tours": 6, "od_questions": 15, "dsl": dsl("od")}),
    ("ksi", "КСИ", {"game_type": "ksi_stickers", "ksi_themes": 16, "dsl": dsl("ksi"), "stickers": {
        "neutral": {"color": "#ffffff", "max": 7}, "x2": {"color": "#fdf66f", "max": 3},
        "nowrong": {"color": "#aded87", "max": 3}, "emptywrong": {"color": "#ff7a6b", "max": 3}}}),
    ("si-stud", "СИ. Студенческий зачёт", {"game_type": "si", "dsl": dsl("si-students")}),
    ("si-mol", "СИ. Молодёжный зачёт", {"game_type": "si", "dsl": dsl("si-youth")}),
    ("kinsbf-stud", "КИнСбФ. Студенческий зачёт", {"game_type": "brain", "dsl": dsl("kinsbf-students")}),
    ("kinsbf-molfest", "КИнСбФ. Молодёжно-фестивальный зачёт", {"game_type": "brain", "dsl": dsl("kinsbf-youth")}),
]
only = sys.argv[4:]
for slug, title, body in games:
    if only and slug not in only:
        continue
    g = api("POST", f"/api/fest/{fest}/games", body)
    gid = g["id"]
    api("PATCH", f"/api/fest/{fest}/games/{gid}/settings", {"title": title, "slug": slug})
    print(gid, slug, title)
