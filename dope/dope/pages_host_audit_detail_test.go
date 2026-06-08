package main

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

// gamesAuditRow builds an audit before/after payload for a games row: the row
// JSON with state_json embedded as a (string) column, matching what the audit
// trigger records.
func gamesAuditRow(t *testing.T, state map[string]any) sql.NullString {
	t.Helper()
	stateJSON, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	row, err := json.Marshal(map[string]any{
		"id":         2,
		"code":       "ksi1",
		"title":      "Своя игра",
		"game_type":  "ksi",
		"state_json": string(stateJSON),
	})
	if err != nil {
		t.Fatal(err)
	}
	return sql.NullString{String: string(row), Valid: true}
}

func ksiState(parts []string, answers map[[3]int]string) map[string]any {
	// themes[t].answers[team][q]; build a 2-theme x len(parts) x 5 grid.
	themes := make([]any, 2)
	for ti := range themes {
		teams := make([]any, len(parts))
		for team := range teams {
			teams[team] = []any{"", "", "", "", ""}
		}
		themes[ti] = map[string]any{"answers": teams}
	}
	for k, v := range answers {
		ti, team, q := k[0], k[1], k[2]
		themes[ti].(map[string]any)["answers"].([]any)[team].([]any)[q] = v
	}
	psParts := make([]any, len(parts))
	for i, p := range parts {
		psParts[i] = p
	}
	return map[string]any{"finished": false, "participants": psParts, "themes": themes}
}

func TestGamesRowChangesRendersKSICellClear(t *testing.T) {
	parts := []string{"Альфа", "Бета", "Гамма"}
	before := gamesAuditRow(t, ksiState(parts, map[[3]int]string{{1, 2, 4}: "right"}))
	after := gamesAuditRow(t, ksiState(parts, map[[3]int]string{}))
	lines := gamesRowChanges(nil, before, after)
	if len(lines) != 1 {
		t.Fatalf("want 1 change, got %d: %v", len(lines), lines)
	}
	want := "Своя игра — Тема 2 · Вопрос 5 · «Гамма»: верно → пусто"
	if lines[0] != want {
		t.Fatalf("want %q, got %q", want, lines[0])
	}
}

func TestGamesRowChangesRendersSetAndFinished(t *testing.T) {
	parts := []string{"Альфа", "Бета"}
	bs := ksiState(parts, map[[3]int]string{})
	as := ksiState(parts, map[[3]int]string{{0, 1, 0}: "wrong"})
	as["finished"] = true
	lines := gamesRowChanges(nil, gamesAuditRow(t, bs), gamesAuditRow(t, as))
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Тема 1 · Вопрос 1 · «Бета»: пусто → неверно") {
		t.Fatalf("missing cell set line: %v", lines)
	}
	if !strings.Contains(joined, "Игра отмечена завершённой") {
		t.Fatalf("missing finished line: %v", lines)
	}
}

func TestMatchRowChangesRendersStatusToggle(t *testing.T) {
	mk := func(status string) sql.NullString {
		row, _ := json.Marshal(map[string]any{"id": 11, "title": "1/8 №3", "status": status})
		return sql.NullString{String: string(row), Valid: true}
	}
	finish := matchRowChanges(nil, mk("active"), mk("finished"))
	if len(finish) != 1 || !strings.Contains(finish[0], "отмечен законченным") || !strings.Contains(finish[0], "1/8 №3") {
		t.Fatalf("finish toggle: %v", finish)
	}
	resume := matchRowChanges(nil, mk("finished"), mk("active"))
	if len(resume) != 1 || !strings.Contains(resume[0], "снята отметка") {
		t.Fatalf("resume toggle: %v", resume)
	}
	none := matchRowChanges(nil, mk("active"), mk("active"))
	if len(none) != 0 {
		t.Fatalf("no status change should produce no lines: %v", none)
	}
}

// TestAnswerRowChangesLabelsEKMatch drives a real EK cell edit through the write
// path and checks the audit line leads with the match it belongs to (the
// user-facing "Бой «A»" prefix), so EK history is attributable to a bout.
func TestAnswerRowChangesLabelsEKMatch(t *testing.T) {
	srv, scope := newBatchTestServer(t)

	theme, answer := 0, 0
	right := "right"
	if _, _, _, _, err := srv.applyScopedMatchUpdate(t.Context(), scope,
		[]updateRequest{{Team: 0, Theme: &theme, Answer: &answer, Mark: &right}}); err != nil {
		t.Fatalf("apply edit: %v", err)
	}

	var title string
	if err := srv.db.QueryRow(`select title from matches where id = ?`, scope.MatchID).Scan(&title); err != nil {
		t.Fatalf("match title: %v", err)
	}

	rows := loadAuditRows(t, srv.db, "answers")
	if len(rows) == 0 {
		t.Fatal("no answers audit rows recorded for the EK edit")
	}
	res := newAuditMatchResolver(t.Context(), srv.db)
	last := rows[len(rows)-1]
	lines := answerRowChanges(res, last.BeforeJSON, last.AfterJSON)
	if len(lines) != 1 {
		t.Fatalf("want 1 change line, got %d: %v", len(lines), lines)
	}
	wantMatch := "Бой «" + title + "»"
	if !strings.HasPrefix(lines[0], wantMatch) {
		t.Fatalf("line should lead with the match %q: %q", wantMatch, lines[0])
	}
	if !strings.Contains(lines[0], "вопрос 1") || !strings.Contains(lines[0], "верно") {
		t.Fatalf("line should show the cell and mark: %q", lines[0])
	}
}
