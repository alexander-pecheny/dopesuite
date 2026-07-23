package store

import (
	"encoding/json"
	"testing"
)

// The blob keys team sections by team id so reseeds can never reshuffle state
// and journal patches address a stable path. Sections appear on first touch.
func TestMatchBlobRoundTrip(t *testing.T) {
	blob := MatchBlob{}
	blob.SetAnswer(17, "regular", 2, 4, "right")
	blob.SetAnswer(17, "regular", 2, 4, "right") // idempotent
	blob.SetPlayer(17, "regular", 0, 55)

	raw, err := json.Marshal(blob)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back MatchBlob
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	section := back.Teams["17"]
	if section == nil {
		t.Fatalf("team 17 section missing: %s", raw)
	}
	if len(section.Themes) != 3 {
		t.Fatalf("themes grew to %d, want 3 (index 2 touched)", len(section.Themes))
	}
	if section.Themes[2].Answers[4] != "right" || section.Themes[0].Player != 55 {
		t.Fatalf("section = %+v", section)
	}
	if back.Team(99) == nil || len(back.Teams["99"].Themes) != 0 {
		t.Fatal("untouched team section should exist empty after Team()")
	}
}

// Shootout themes append and remove across every seated team in lockstep.
func TestMatchBlobShootout(t *testing.T) {
	blob := MatchBlob{}
	blob.Team(1)
	blob.Team(2)
	if n := blob.AddShootoutTheme([]int64{1, 2}); n != 0 {
		t.Fatalf("first shootout index = %d, want 0", n)
	}
	if n := blob.AddShootoutTheme([]int64{1, 2}); n != 1 {
		t.Fatalf("second shootout index = %d, want 1", n)
	}
	blob.SetAnswer(1, "shootout", 1, 0, "wrong")
	if err := blob.RemoveShootoutTheme(); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(blob.Teams["1"].ShootoutThemes) != 1 || len(blob.Teams["2"].ShootoutThemes) != 1 {
		t.Fatalf("shootout counts after remove: %d/%d, want 1/1",
			len(blob.Teams["1"].ShootoutThemes), len(blob.Teams["2"].ShootoutThemes))
	}
	if err := blob.RemoveShootoutTheme(); err != nil {
		t.Fatalf("remove second: %v", err)
	}
	if blob.RemoveShootoutTheme() == nil {
		t.Fatal("removing from empty shootout should error")
	}
}

// TeamStateFromBlob projects a blob section into the legacy TeamState shape:
// theme players resolve id → display name, the grid pads to ThemeCount, and
// marks normalise — so BuildView and every downstream consumer are unchanged.
func TestTeamStateFromBlob(t *testing.T) {
	blob := MatchBlob{}
	blob.SetPlayer(7, "regular", 1, 55)
	blob.SetAnswer(7, "regular", 1, 0, "+")
	blob.SetAnswer(7, "shootout", 0, 2, "Q")
	section := blob.Teams["7"]

	names := map[int64]string{55: "Анна Б."}
	team := TeamStateFromBlob(section, "Команда", []string{"Анна Б."}, 2.0,
		func(id int64) string { return names[id] })
	if team.Name != "Команда" || team.Place != 2.0 || len(team.Roster) != 1 {
		t.Fatalf("identity fields: %+v", team)
	}
	if len(team.Themes) != ThemeCount {
		t.Fatalf("themes padded to %d, want %d", len(team.Themes), ThemeCount)
	}
	if team.Themes[1].Player != "Анна Б." || team.Themes[1].Answers[0] != "right" {
		t.Fatalf("theme 1 = %+v", team.Themes[1])
	}
	if len(team.ShootoutThemes) != 1 || team.ShootoutThemes[0].Answers[2] != "right" {
		t.Fatalf("shootout = %+v", team.ShootoutThemes)
	}
}
