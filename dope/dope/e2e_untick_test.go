package dopeserver

import (
	"context"
	"database/sql"
	"dope/dope/realtime"
	"fmt"
	"os"
	"testing"
)

// TestE2EUntickEditRetickRealDB drives the real operator workflow — untick a
// finished EK bout, edit a score, re-tick it — against an external database via
// the exact server write path (applyScopedMatchUpdate), and asserts that every
// OTHER bout in the game keeps its protocol data byte-for-byte. It is gated on
// E2E_DB so it only runs against a copy of a real DB; without it, it skips.
//
//	E2E_DB=/path/to/copy.db E2E_FEST=6 E2E_GAME=8 E2E_MATCH=H \
//	  go test ./dope -run TestE2EUntickEditRetickRealDB -v
func TestE2EUntickEditRetickRealDB(t *testing.T) {
	path := os.Getenv("E2E_DB")
	if path == "" {
		t.Skip("set E2E_DB to a real-DB copy to run this end-to-end check")
	}
	festID := envInt64("E2E_FEST", 6)
	gameID := envInt64("E2E_GAME", 8)
	matchCode := envOr("E2E_MATCH", "H")

	db, err := openFestDB(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	srv := &server{
		db:              db,
		rt:              realtime.NewManager(),
	}
	ctx := context.Background()
	scopeBase := festScope{FestID: festID, GameID: gameID}

	scope, err := srv.verifyMatchInScope(ctx, scopeBase, matchCode)
	if err != nil {
		t.Fatalf("scope %s: %v", matchCode, err)
	}
	apply := func(req updateRequest, what string) {
		t.Helper()
		if _, _, _, _, err := srv.applyScopedMatchUpdate(ctx, scope, []updateRequest{req}); err != nil {
			t.Fatalf("%s on %s: %v", what, matchCode, err)
		}
	}
	tr, fa := true, false

	// 1) A pure untick→retick (no edit) must be a true no-op: every other bout's
	//    protocol data is byte-identical afterwards.
	base := gameAnswersExcept(t, db, gameID, matchCode)
	if len(base) == 0 {
		t.Fatalf("no protocol data found for game %d (besides %s)", gameID, matchCode)
	}
	apply(updateRequest{Finished: &fa}, "untick")
	apply(updateRequest{Finished: &tr}, "retick")
	if got := gameAnswersExcept(t, db, gameID, matchCode); !mapsEqual(base, got) {
		t.Fatalf("pure untick→retick of %s was not a no-op downstream", matchCode)
	}

	// 2) untick→edit→retick must never DELETE downstream data. A score edit that
	//    changes who advances may legitimately ADD a newly-advancing team's (empty)
	//    themes downstream — that is correct reactive behavior — but no previously
	//    entered row may disappear. The old resolver wiped it; this one must not.
	before := gameAnswersExcept(t, db, gameID, matchCode)
	theme, ans, mark := 0, 4, "wrong"
	apply(updateRequest{Finished: &fa}, "untick")
	apply(updateRequest{Team: 0, Theme: &theme, Answer: &ans, Mark: &mark}, "edit")
	apply(updateRequest{Finished: &tr}, "retick")
	after := gameAnswersExcept(t, db, gameID, matchCode)

	var removed int
	for k, v := range before {
		if av, ok := after[k]; !ok || av != v {
			removed++
			if removed <= 5 {
				t.Logf("LOST downstream cell %s (was %q, now %q/%v)", k, v, after[k], after[k])
			}
		}
	}
	if removed > 0 {
		t.Fatalf("untick→edit→retick of %s deleted/changed %d existing downstream cells (data loss)", matchCode, removed)
	}
	t.Logf("ok: %d→%d downstream cells, none removed (additions are allowed)", len(before), len(after))
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func gameAnswersExcept(t *testing.T, db *sql.DB, gameID int64, exceptCode string) map[string]string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
select m.code, th.team_id, th.kind, th.theme_index, a.answer_index, a.mark
from answers a
join themes th on th.id = a.theme_id
join matches m on m.id = th.match_id
where m.game_id = ? and m.code <> ?
order by m.code, th.team_id, th.kind, th.theme_index, a.answer_index`, gameID, exceptCode)
	if err != nil {
		t.Fatalf("snapshot answers: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var code, kind, mark string
		var team int64
		var ti, ai int
		if err := rows.Scan(&code, &team, &kind, &ti, &ai, &mark); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[fmt.Sprintf("%s/%d/%s/%d/%d", code, team, kind, ti, ai)] = mark
	}
	return out
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
