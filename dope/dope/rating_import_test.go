package main

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

type ksiTestState struct {
	Participants []string `json:"participants"`
	Themes       []struct {
		Answers [][]string `json:"answers"`
	} `json:"themes"`
}

func TestRemapAnswerMatrixFollowsTeams(t *testing.T) {
	old := [][]string{
		{"", "", "", "", ""},  // A
		{"", "x", "", "", ""}, // B
		{"", "", "", "y", ""}, // C
	}
	oldNames := []string{"A", "B", "C"}
	newNames := []string{"C", "B", "D"} // A removed, D added, order changed

	out := remapAnswerMatrix(old, oldNames, newNames, 5)
	if len(out) != 3 {
		t.Fatalf("want 3 rows, got %d: %v", len(out), out)
	}
	if out[0][3] != "y" {
		t.Fatalf("C's mark should move to its new index 0: %v", out)
	}
	if out[1][1] != "x" {
		t.Fatalf("B's mark should stay with B at index 1: %v", out)
	}
	for _, v := range out[2] {
		if v != "" {
			t.Fatalf("new team D should start empty: %v", out[2])
		}
	}
}

func TestRemapAnswerMatrixLegacyNoNamesResizesPositionally(t *testing.T) {
	old := [][]string{{"a"}, {"b"}}
	out := remapAnswerMatrix(old, nil, []string{"X", "Y", "Z"}, 2)
	if len(out) != 3 || out[0][0] != "a" || out[1][0] != "b" || out[2][0] != "" {
		t.Fatalf("legacy state (no names) should resize positionally: %v", out)
	}
}

// TestImportRatingRosterRemapsKSIScoresByTeam guards the data-integrity bug where
// the KSI answer grid is keyed by row position: re-importing a roster that drops
// one team and adds another shifts the alphabetical positions of the survivors,
// so their scores must follow them by identity instead of staying at a fixed row.
func TestImportRatingRosterRemapsKSIScoresByTeam(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, _, ksiGameID := createRosterPropagationFixture(t, db)
	srv := &server{db: db, subscribers: make(map[int64]map[chan event]bool)}

	initial := []festRosterImportTeam{
		{RatingID: 2, Name: "Бета"},
		{RatingID: 3, Name: "Гамма"},
		{RatingID: 5, Name: "Эхо"},
	}
	if _, err := srv.importFestRoster(t.Context(), festID, 13533, initial); err != nil {
		t.Fatalf("initial import: %v", err)
	}

	// Inject scores keyed to specific teams at their post-import positions.
	before := loadKSIState(t, db, ksiGameID)
	idx := nameIndex(before.Participants)
	grid := make([][]string, len(before.Participants))
	for i := range grid {
		grid[i] = []string{"", "", "", "", ""}
	}
	grid[idx["Гамма"]][2] = "right"
	grid[idx["Эхо"]][4] = "wrong"
	if _, err := db.Exec(`update games set state_json = ? where id = ?`, mustJSON(map[string]any{
		"participants": before.Participants,
		"themes":       []map[string]any{{"answers": grid}},
		"finished":     false,
	}), ksiGameID); err != nil {
		t.Fatalf("seed scores: %v", err)
	}

	// Re-import: drop "Бета", add "Яков" — this moves "Гамма" and "Эхо" to new rows.
	next := []festRosterImportTeam{
		{RatingID: 3, Name: "Гамма"},
		{RatingID: 5, Name: "Эхо"},
		{RatingID: 9, Name: "Яков"},
	}
	if _, err := srv.importFestRoster(t.Context(), festID, 13533, next); err != nil {
		t.Fatalf("re-import: %v", err)
	}

	after := loadKSIState(t, db, ksiGameID)
	idx2 := nameIndex(after.Participants)
	answers := after.Themes[0].Answers

	if got := answers[idx2["Гамма"]][2]; got != "right" {
		t.Fatalf("Гамма should keep its score after the shift, got %q", got)
	}
	if got := answers[idx2["Эхо"]][4]; got != "wrong" {
		t.Fatalf("Эхо should keep its score after the shift, got %q", got)
	}
	// Regression guard: under the old positional resize, Гамма's mark would land
	// on whoever now occupies its old row index — Эхо must not inherit it.
	if got := answers[idx2["Эхо"]][2]; got != "" {
		t.Fatalf("Эхо must not inherit Гамма's score, got %q", got)
	}
	for q, v := range answers[idx2["Яков"]] {
		if v != "" {
			t.Fatalf("new team Яков should start empty, q%d = %q", q, v)
		}
	}
}

func loadKSIState(t *testing.T, db *sql.DB, gameID int64) ksiTestState {
	t.Helper()
	var raw string
	if err := db.QueryRow(`select state_json from games where id = ?`, gameID).Scan(&raw); err != nil {
		t.Fatalf("load ksi state: %v", err)
	}
	var st ksiTestState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatalf("unmarshal ksi state: %v", err)
	}
	if len(st.Themes) == 0 {
		t.Fatalf("ksi state has no themes: %s", raw)
	}
	return st
}

func nameIndex(names []string) map[string]int {
	idx := make(map[string]int, len(names))
	for i, n := range names {
		idx[n] = i
	}
	return idx
}
