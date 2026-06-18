package dopeserver

import (
	"context"
	"database/sql"
	"dope/dope/realtime"
	"dope/dope/store"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestResolverPropagatesBracket exercises the full forward chain: finishing the
// 1/16 and 1/8 bouts makes the reseed ready but still empty, the explicit
// calculate action sums both games and resolves downstream slots, and
// un-finishing an upstream bout rolls it all back.
func TestResolverPropagatesBracket(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, gameID := createBracketFixture(t, db)
	srv := &server{
		db: db,
		rt: realtime.NewManager(),
	}
	scopeBase := festScope{FestID: festID, GameID: gameID}
	if _, _, _, err := srv.importSeedsFromKSI(t.Context(), scopeBase); err != nil {
		t.Fatalf("import seeds: %v", err)
	}
	assertReseedState(t, loadReseedStageView(t, srv, festID, gameID, "rs"), false, []string{"A", "B"}, "Бои A, B не закончены")
	if _, _, _, err := srv.calculateScopedReseed(t.Context(), scopeBase, "rs"); !errors.Is(err, errReseedNotReady) {
		t.Fatalf("calculate reseed before sources finished error = %v, want errReseedNotReady", err)
	} else if got, want := err.Error(), "Бои A, B не закончены"; got != want {
		t.Fatalf("calculate reseed before sources finished error = %q, want %q", got, want)
	}

	finish := func(code string, finished bool) {
		t.Helper()
		scope, err := srv.verifyMatchInScope(t.Context(), scopeBase, code)
		if err != nil {
			t.Fatalf("scope %s: %v", code, err)
		}
		if _, _, _, _, err := srv.applyScopedMatchUpdate(t.Context(), scope, []updateRequest{{Finished: &finished}}); err != nil {
			t.Fatalf("finish %s=%v: %v", code, finished, err)
		}
	}
	calculate := func() {
		t.Helper()
		if _, _, _, err := srv.calculateScopedReseed(t.Context(), scopeBase, "rs"); err != nil {
			t.Fatalf("calculate reseed: %v", err)
		}
	}
	// mark answers a single question (value index 4 == +50) for one team slot.
	mark := func(code string, teamIndex, theme, answer int, value string) {
		t.Helper()
		scope, err := srv.verifyMatchInScope(t.Context(), scopeBase, code)
		if err != nil {
			t.Fatalf("scope %s: %v", code, err)
		}
		if _, _, _, _, err := srv.applyScopedMatchUpdate(t.Context(), scope, []updateRequest{{Team: teamIndex, Theme: &theme, Answer: &answer, Mark: &value}}); err != nil {
			t.Fatalf("mark %s team %d: %v", code, teamIndex, err)
		}
	}

	// One correct +50 (answer index 4) in the 1/16 — its pair in the 1/8 is
	// entered once B's slots resolve, below — so the reseed's summed correct_50
	// column is non-zero and actually exercised.
	mark("A", 0, 0, 4, "right")

	// Only the 1/16 bout is done — reseed cannot be computed and the 1/4 slot
	// stays unresolved.
	finish("A", true)
	assertReseedState(t, loadReseedStageView(t, srv, festID, gameID, "rs"), false, []string{"B"}, "Бой B не закончен")
	if n := reseedEntryCount(t, db, gameID, "rs"); n != 0 {
		t.Fatalf("reseed entries before 1/8 finished = %d, want 0", n)
	}
	if teams := slotTeams(t, db, gameID, "C"); !allZero(teams) {
		t.Fatalf("1/4 slots resolved too early: %v", teams)
	}

	// Both games done — the reseed is ready, but still not calculated.
	mark("B", 0, 0, 4, "right")
	finish("B", true)
	if n := reseedEntryCount(t, db, gameID, "rs"); n != 0 {
		t.Fatalf("reseed entries before manual calculation = %d, want 0", n)
	}
	if teams := slotTeams(t, db, gameID, "C"); !allZero(teams) {
		t.Fatalf("1/4 slots resolved before manual reseed calculation: %v", teams)
	}
	assertReseedState(t, loadReseedStageView(t, srv, festID, gameID, "rs"), true, nil, "")

	// Manual calculation sums place across A and B (1+1, 2+2, ...) and the 1/4
	// bout's reseed slots resolve to the ranked teams.
	calculate()
	entries := reseedEntries(t, db, gameID, "rs")
	if len(entries) != 4 {
		t.Fatalf("reseed entries = %d, want 4", len(entries))
	}
	if got := entries[0].num("place_sum"); got != 2 {
		t.Fatalf("rank 1 place_sum = %v, want 2 (place 1 in each of two games)", got)
	}
	var totalCorrect50 float64
	for _, e := range entries {
		totalCorrect50 += e.num("correct_50")
	}
	if totalCorrect50 != 2 {
		t.Fatalf("summed correct_50 across reseed = %v, want 2 (one +50 in each game)", totalCorrect50)
	}
	cTeams := slotTeams(t, db, gameID, "C")
	if allZero(cTeams) {
		t.Fatalf("1/4 slots not resolved after both games finished: %v", cTeams)
	}
	if cTeams[0] != entries[0].teamID {
		t.Fatalf("1/4 first slot = team %d, want reseed rank 1 team %d", cTeams[0], entries[0].teamID)
	}
	if got := regularThemeCount(t, db, gameID, "C", cTeams[0]); got != store.ThemeCount {
		t.Fatalf("resolved 1/4 team has %d regular themes, want %d", got, store.ThemeCount)
	}

	// Reopening the 1/16 to edit it is NON-DESTRUCTIVE: the calculated reseed is
	// held (not wiped), and the downstream 1/4 keeps its resolved teams and their
	// protocol data — so an untick→edit→retick loses nothing. The reseed merely
	// reports "not ready" (recomputed live) until the source is finished again.
	finish("A", false)
	if n := reseedEntryCount(t, db, gameID, "rs"); n != 4 {
		t.Fatalf("reseed entries held after reopening 1/16 = %d, want 4 (non-destructive)", n)
	}
	// Only A is unfinished now (B stays finished and untouched — the old
	// destructive cascade used to disturb B's branch too, reporting [A, B]).
	assertReseedState(t, loadReseedStageView(t, srv, festID, gameID, "rs"), false, []string{"A"}, "Бой A не закончен")
	if teams := slotTeams(t, db, gameID, "C"); allZero(teams) {
		t.Fatalf("1/4 slots wrongly cleared after reopening 1/16: %v", teams)
	}
	if got := regularThemeCount(t, db, gameID, "C", cTeams[0]); got != store.ThemeCount {
		t.Fatalf("downstream themes deleted after reopening 1/16: %d, want %d (non-destructive)", got, store.ThemeCount)
	}

	// Re-finishing restores the identical downstream state — a true no-op.
	finish("A", true)
	if n := reseedEntryCount(t, db, gameID, "rs"); n != 4 {
		t.Fatalf("reseed entries after re-finishing 1/16 = %d, want 4", n)
	}
	reTeams := slotTeams(t, db, gameID, "C")
	for i := range cTeams {
		if reTeams[i] != cTeams[i] {
			t.Fatalf("1/4 slot %d changed across untick/retick: %d != %d", i, reTeams[i], cTeams[i])
		}
	}
}

// TestMatchUpdateBroadcastsCascade verifies that explicit reseed calculation
// reports downstream matches as `cascaded`, so the handler can broadcast them
// and spectators see advancing teams without a reload.
func TestMatchUpdateBroadcastsCascade(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, gameID := createBracketFixture(t, db)
	srv := &server{
		db: db,
		rt: realtime.NewManager(),
	}
	scopeBase := festScope{FestID: festID, GameID: gameID}
	if _, _, _, err := srv.importSeedsFromKSI(t.Context(), scopeBase); err != nil {
		t.Fatalf("import seeds: %v", err)
	}

	apply := func(code string, req updateRequest) []store.MatchView {
		t.Helper()
		scope, err := srv.verifyMatchInScope(t.Context(), scopeBase, code)
		if err != nil {
			t.Fatalf("scope %s: %v", code, err)
		}
		_, _, _, cascaded, err := srv.applyScopedMatchUpdate(t.Context(), scope, []updateRequest{req})
		if err != nil {
			t.Fatalf("apply %s: %v", code, err)
		}
		return cascaded
	}
	tr := true
	theme, answer := 0, 4
	right := "right"

	// Only the 1/16 done so far — the 1/4 reseed needs both games, so C stays
	// unresolved and must NOT appear in any cascade yet.
	apply("A", updateRequest{Team: 0, Theme: &theme, Answer: &answer, Mark: &right})
	for _, v := range apply("A", updateRequest{Finished: &tr}) {
		if v.Code == "C" {
			t.Fatalf("1/4 match C cascaded before its reseed could compute")
		}
	}

	// Finishing the 1/8 completes both games, but the 1/4 reseed slots stay empty
	// until the explicit calculate action.
	apply("B", updateRequest{Team: 0, Theme: &theme, Answer: &answer, Mark: &right})
	for _, v := range apply("B", updateRequest{Finished: &tr}) {
		if v.Code == "C" {
			t.Fatalf("1/4 match C cascaded before manual reseed calculation")
		}
	}
	_, cascaded, _, err := srv.calculateScopedReseed(t.Context(), scopeBase, "rs")
	if err != nil {
		t.Fatalf("calculate reseed: %v", err)
	}
	var cView *store.MatchView
	for i := range cascaded {
		if cascaded[i].Code == "C" {
			cView = &cascaded[i]
		}
	}
	if cView == nil {
		got := make([]string, 0, len(cascaded))
		for _, v := range cascaded {
			got = append(got, v.Code)
		}
		t.Fatalf("finishing the 1/8 did not cascade the 1/4 match C; cascaded=%v", got)
	}
	if len(cView.Teams) == 0 {
		t.Fatalf("cascaded match C carried no teams view")
	}
}

// TestAssignDrawLots covers the deterministic Жребий lottery: only true ties draw
// lots, each team's lot is a stable function of the game seed, and recomputing
// (in any input order) yields identical lots and order. Untied teams stay at 0.
func TestAssignDrawLots(t *testing.T) {
	rules := []reseedSortRule{
		{Metric: "place_sum", Dir: "asc"},
		{Metric: "total", Dir: "desc"},
		{Metric: "draw", Dir: "desc"},
	}
	mk := func(team int64, place, total float64) reseedEntry {
		return reseedEntry{teamID: team, metrics: map[string]float64{"place_sum": place, "total": total}}
	}
	const seed = "seed-abc123"
	// Teams 1,2,3 tie completely; team 4 stands alone.
	entries := []reseedEntry{mk(1, 3, 100), mk(2, 3, 100), mk(3, 3, 100), mk(4, 3, 90)}

	sortReseedEntries(entries, rules)
	assignDrawLots(entries, rules, seed)
	sortReseedEntries(entries, rules)

	draws := map[int64]float64{}
	for _, e := range entries {
		draws[e.teamID] = e.metrics["draw"]
	}
	if draws[4] != 0 {
		t.Fatalf("untied team 4 got a lot %v, want 0", draws[4])
	}
	for _, team := range []int64{1, 2, 3} {
		if draws[team] == 0 {
			t.Fatalf("tied team %d got no lot", team)
		}
		if want := float64(deterministicLot(seed, team)); draws[team] != want {
			t.Fatalf("team %d lot %v, want deterministic %v", team, draws[team], want)
		}
	}

	// Recompute in a different input order with the SAME seed: order and lots must
	// be byte-identical (this is what makes untick/retick a no-op for reseed).
	next := []reseedEntry{mk(3, 3, 100), mk(1, 3, 100), mk(2, 3, 100), mk(4, 3, 90)}
	sortReseedEntries(next, rules)
	assignDrawLots(next, rules, seed)
	sortReseedEntries(next, rules)
	for idx, e := range next {
		if e.metrics["draw"] != draws[e.teamID] {
			t.Fatalf("team %d lot changed across recompute: %v != %v", e.teamID, e.metrics["draw"], draws[e.teamID])
		}
		if e.teamID != entries[idx].teamID {
			t.Fatalf("order changed across recompute at %d: %d != %d", idx, e.teamID, entries[idx].teamID)
		}
	}

	// A different seed produces a different lottery (so the seed actually matters).
	other := []reseedEntry{mk(1, 3, 100), mk(2, 3, 100), mk(3, 3, 100)}
	assignDrawLots(other, rules, "seed-xyz789")
	allSame := true
	for _, e := range other {
		if e.metrics["draw"] != draws[e.teamID] {
			allSame = false
			break
		}
	}
	if allSame {
		t.Fatalf("different seed produced identical lots — seed not influencing lottery")
	}
}

func allZero(ids []int64) bool {
	for _, id := range ids {
		if id != 0 {
			return false
		}
	}
	return true
}

type reseedRow struct {
	teamID  int64
	metrics map[string]any
}

func (r reseedRow) num(key string) float64 {
	value, _ := r.metrics[key].(float64)
	return value
}

func reseedEntries(t *testing.T, db *sql.DB, gameID int64, stageCode string) []reseedRow {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
select re.team_id, re.metrics_json
from reseed_entries re
join stages s on s.id = re.stage_id
where s.game_id = ? and s.code = ?
order by re.rank`, gameID, stageCode)
	if err != nil {
		t.Fatalf("query reseed: %v", err)
	}
	defer rows.Close()
	var out []reseedRow
	for rows.Next() {
		var r reseedRow
		var raw string
		if err := rows.Scan(&r.teamID, &raw); err != nil {
			t.Fatalf("scan reseed: %v", err)
		}
		if err := json.Unmarshal([]byte(raw), &r.metrics); err != nil {
			t.Fatalf("decode metrics: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func reseedEntryCount(t *testing.T, db *sql.DB, gameID int64, stageCode string) int {
	t.Helper()
	return len(reseedEntries(t, db, gameID, stageCode))
}

func loadReseedStageView(t *testing.T, srv *server, festID, gameID int64, code string) store.StageView {
	t.Helper()
	srv.mu.RLock()
	view, err := srv.loadFestViewLocked(festID, gameID)
	srv.mu.RUnlock()
	if err != nil {
		t.Fatalf("load fest view: %v", err)
	}
	for _, stage := range view.Stages {
		if stage.Code == code {
			return stage
		}
	}
	t.Fatalf("reseed stage %s not found", code)
	return store.StageView{}
}

func assertReseedState(t *testing.T, stage store.StageView, ready bool, pending []string, message string) {
	t.Helper()
	if stage.ReseedReady != ready {
		t.Fatalf("reseed ready = %v, want %v", stage.ReseedReady, ready)
	}
	if !slices.Equal(stage.ReseedPending, pending) {
		t.Fatalf("reseed pending = %v, want %v", stage.ReseedPending, pending)
	}
	if stage.ReseedMessage != message {
		t.Fatalf("reseed message = %q, want %q", stage.ReseedMessage, message)
	}
}

func slotTeams(t *testing.T, db *sql.DB, gameID int64, matchCode string) []int64 {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
select coalesce(ms.team_id, 0)
from match_slots ms
join matches m on m.id = ms.match_id
where m.game_id = ? and m.code = ?
order by ms.slot_index`, gameID, matchCode)
	if err != nil {
		t.Fatalf("query slots: %v", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan slot: %v", err)
		}
		out = append(out, id)
	}
	return out
}

func regularThemeCount(t *testing.T, db *sql.DB, gameID int64, matchCode string, teamID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(), `
select count(*)
from themes th
join matches m on m.id = th.match_id
where m.game_id = ? and m.code = ? and th.team_id = ? and th.kind = 'regular'`,
		gameID, matchCode, teamID).Scan(&count); err != nil {
		t.Fatalf("count themes: %v", err)
	}
	return count
}

// matchAnswerSnapshot returns a stable, comparable dump of every answer mark in a
// match (joined through its themes), so a test can assert protocol data is
// byte-identical before and after an operation.
func matchAnswerSnapshot(t *testing.T, db *sql.DB, gameID int64, matchCode string) string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
select th.team_id, th.kind, th.theme_index, a.answer_index, a.mark
from answers a
join themes th on th.id = a.theme_id
join matches m on m.id = th.match_id
where m.game_id = ? and m.code = ?
order by th.team_id, th.kind, th.theme_index, a.answer_index`, gameID, matchCode)
	if err != nil {
		t.Fatalf("snapshot answers: %v", err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var team int64
		var kind, mark string
		var ti, ai int
		if err := rows.Scan(&team, &kind, &ti, &ai, &mark); err != nil {
			t.Fatalf("scan answer: %v", err)
		}
		fmt.Fprintf(&b, "%d/%s/%d/%d=%q\n", team, kind, ti, ai, mark)
	}
	return b.String()
}

// TestUntickEditRetickPreservesDownstream is the headline guarantee: with a
// finished bracket, unticking an upstream bout to edit a score and re-ticking it
// (without an explicit reseed recalculation — exactly the operator workflow that
// triggered the original data loss) leaves every downstream bout's protocol data
// untouched.
func TestUntickEditRetickPreservesDownstream(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, gameID := createBracketFixture(t, db)
	srv := &server{
		db: db,
		rt: realtime.NewManager(),
	}
	scopeBase := festScope{FestID: festID, GameID: gameID}
	if _, _, _, err := srv.importSeedsFromKSI(t.Context(), scopeBase); err != nil {
		t.Fatalf("import seeds: %v", err)
	}

	apply := func(code string, req updateRequest) {
		t.Helper()
		scope, err := srv.verifyMatchInScope(t.Context(), scopeBase, code)
		if err != nil {
			t.Fatalf("scope %s: %v", code, err)
		}
		if _, _, _, _, err := srv.applyScopedMatchUpdate(t.Context(), scope, []updateRequest{req}); err != nil {
			t.Fatalf("apply %s: %v", code, err)
		}
	}
	tr, fa := true, false
	theme, ans, right, wrong := 0, 4, "right", "wrong"

	// Finish both source bouts and calculate the reseed so the 1/4 (C) resolves.
	apply("A", updateRequest{Team: 0, Theme: &theme, Answer: &ans, Mark: &right})
	apply("A", updateRequest{Finished: &tr})
	apply("B", updateRequest{Team: 0, Theme: &theme, Answer: &ans, Mark: &right})
	apply("B", updateRequest{Finished: &tr})
	if _, _, _, err := srv.calculateScopedReseed(t.Context(), scopeBase, "rs"); err != nil {
		t.Fatalf("calculate reseed: %v", err)
	}

	// Enter protocol data into the downstream 1/4 bout C, then snapshot it.
	apply("C", updateRequest{Team: 0, Theme: &theme, Answer: &ans, Mark: &right})
	apply("C", updateRequest{Finished: &tr})
	before := matchAnswerSnapshot(t, db, gameID, "C")
	if before == "" {
		t.Fatal("no downstream protocol data captured for C")
	}

	// The operator workflow: untick A, edit a score, re-tick A — no recalculation.
	apply("A", updateRequest{Finished: &fa})
	apply("A", updateRequest{Team: 1, Theme: &theme, Answer: &ans, Mark: &wrong})
	apply("A", updateRequest{Finished: &tr})

	after := matchAnswerSnapshot(t, db, gameID, "C")
	if before != after {
		t.Fatalf("downstream protocol data changed across untick/edit/retick:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// createBracketFixture builds a fest with a KSI game (for seeding) and an EK
// game whose bracket is 1/16 (A) -> 1/8 (B) -> reseed (rs) -> 1/4 (C).
func createBracketFixture(t *testing.T, db *sql.DB) (int64, int64) {
	t.Helper()
	ctx := t.Context()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	now := utcNow()
	systemID, err := ensureSystemUser(ctx, tx)
	if err != nil {
		t.Fatalf("system user: %v", err)
	}
	festID, err := store.InsertReturningID(ctx, tx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(null, 'Bracket fixture', '', null, ?, 1, ?, ?, 1)`, systemID, now, now)
	if err != nil {
		t.Fatalf("insert fest: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, role, added_at)
values(?, ?, 'creator', ?)`, festID, systemID, now); err != nil {
		t.Fatalf("insert organizer: %v", err)
	}

	answers := [][]string{
		{"", "", "", "right", ""},
		{"", "", "", "", "right"},
		{"", "", "right", "", ""},
		{"", "right", "", "", ""},
	}
	if _, err := insertJSONGameFixture(ctx, tx, festID, "ksi", "КСИ", "ksi", 1,
		map[string]any{
			"schemaVersion": 2, "title": "КСИ", "gameType": "ksi",
			"participants": []string{"A", "B", "C", "D"}, "themes": 1,
		},
		map[string]any{
			"participants": []string{"A", "B", "C", "D"},
			"themes":       []map[string]any{{"answers": answers}},
			"finished":     true,
		}); err != nil {
		t.Fatalf("insert ksi: %v", err)
	}

	rawScheme := `{
	  "schemaVersion": 2,
	  "slug": "bracket-ek",
	  "title": "Bracket EK",
	  "gameType": "ek",
	  "venues": [{"number": 1, "title": "Зал"}],
	  "stages": [
	    {"code": "r16", "title": "1/16", "stage_type": "matches", "position": 1,
	     "matches": [{"code": "A", "title": "Бой A", "venue": 1, "participantCount": 4,
	       "slots": ["seed-1", "seed-2", "seed-3", "seed-4"]}]},
	    {"code": "r8", "title": "1/8", "stage_type": "matches", "position": 2,
	     "matches": [{"code": "B", "title": "Бой B", "venue": 1, "participantCount": 4,
	       "slots": [
	         {"fromMatch": {"match": "A", "place": 1}},
	         {"fromMatch": {"match": "A", "place": 2}},
	         {"fromMatch": {"match": "A", "place": 3}},
	         {"fromMatch": {"match": "A", "place": 4}}]}]},
	    {"code": "rs", "title": "Пересев", "stage_type": "reseed", "position": 3,
	     "teams": [
	       {"fromMatch": {"match": "B", "place": 1}},
	       {"fromMatch": {"match": "B", "place": 2}},
	       {"fromMatch": {"match": "B", "place": 3}},
	       {"fromMatch": {"match": "B", "place": 4}}],
	     "sources": ["r16", "r8"],
	     "sort": [{"metric": "place_sum", "dir": "asc"}, {"metric": "total", "dir": "desc"}]},
	    {"code": "r4", "title": "1/4", "stage_type": "matches", "position": 4,
	     "matches": [{"code": "C", "title": "Бой C", "venue": 1, "participantCount": 4,
	       "slots": [
	         {"reseed": {"stage": "rs", "rank": 1}},
	         {"reseed": {"stage": "rs", "rank": 2}},
	         {"reseed": {"stage": "rs", "rank": 3}},
	         {"reseed": {"stage": "rs", "rank": 4}}]}]}
	  ]
	}`
	var scheme store.FestScheme
	if err := json.Unmarshal([]byte(rawScheme), &scheme); err != nil {
		t.Fatalf("decode ek scheme: %v", err)
	}
	gameID, err := createEKGameTx(ctx, tx, festID, scheme)
	if err != nil {
		t.Fatalf("create ek: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return festID, gameID
}
