"""Loads a flat game's whole document — ОД's entry grid, КСИ's theme grid —
through the same generic set-op path the page itself edits by, so the transfer
is journaled like any other edit rather than written behind the server's back."""
import json, sys, urllib.request

BASE = "http://127.0.0.1:19680"
token = open(".tmp/token").read().strip()


def patch(fest_id, game_id, ops):
    body = json.dumps({"ops": ops}).encode()
    req = urllib.request.Request(f"{BASE}/api/fest/{fest_id}/games/{game_id}/state",
                                 data=body, method="PATCH",
                                 headers={"Content-Type": "application/json",
                                          "Cookie": "session=" + token})
    with urllib.request.urlopen(req) as resp:
        return resp.status


def seat_od_teams(db_path, game_id, teams):
    """ОД's team list is the rating import's to own — the protocol declares it
    immutable under host edits — so it is seated at creation, before any play."""
    import sqlite3
    db = sqlite3.connect(db_path)
    raw = db.execute("select state_json from games where id = ?", (game_id,)).fetchone()[0]
    state = json.loads(raw or "{}")
    state["teams"] = teams
    state.setdefault("entries", [])
    state.setdefault("completed", [])
    # СтудЧР's ОД had 65 teams while its брейн had 48, so this game owns its
    # team list instead of taking the fest's — that is what team_list_source
    # is for.
    db.execute("update games set state_json = ?, team_list_source = 'game' where id = ?",
               (json.dumps(state, ensure_ascii=False), game_id))
    db.commit()


def import_od(fest_id, game_id):
    """ОД's teams come from its fest, the way every flat game's do — that is why
    ОД gets its own fest: at СтудЧР it had 65 teams where the брейн had 48, and
    the two numberings are different identities."""
    data = json.load(open(".tmp/od-data.json"))
    ops = [{"path": ["entries"], "value": data["entries"]},
           {"path": ["completed"], "value": data["completed"]}]
    print("ОД:", patch(fest_id, game_id, ops), f'{len(data["teams"])} команд, {len(data["entries"])} вопросов')


if __name__ == "__main__":
    import_od(int(sys.argv[1]), int(sys.argv[2]))
