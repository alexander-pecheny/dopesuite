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

// Shootout themes are added and removed per team at an index; the editor keeps
// the grid in lockstep by touching every seated team at the same index.
func TestMatchBlobShootout(t *testing.T) {
	blob := MatchBlob{}
	for _, index := range []int{0, 1} {
		blob.EnsureTheme(1, "shootout", index)
		blob.EnsureTheme(2, "shootout", index)
	}
	blob.SetAnswer(1, "shootout", 1, 0, "wrong")
	blob.RemoveTheme(1, "shootout", 1)
	blob.RemoveTheme(2, "shootout", 1)
	if len(blob.Teams["1"].ShootoutThemes) != 1 || len(blob.Teams["2"].ShootoutThemes) != 1 {
		t.Fatalf("shootout counts after remove: %d/%d, want 1/1",
			len(blob.Teams["1"].ShootoutThemes), len(blob.Teams["2"].ShootoutThemes))
	}
	blob.RemoveTheme(1, "shootout", 5) // out of range no-ops
	if len(blob.Teams["1"].ShootoutThemes) != 1 {
		t.Fatal("out-of-range remove should no-op")
	}
}

// A pin is the host's manual place and rides in the blob as Protocol state, so
// it survives the round trip and wins over the scored place at projection time.
func TestMatchBlobPin(t *testing.T) {
	blob := MatchBlob{}
	place := 1.0
	blob.SetPin(7, &place)
	if got := blob.Pin(7); got == nil || *got != 1 {
		t.Fatalf("pin = %v, want 1", got)
	}
	team := TeamStateFromBlob(blob.Teams["7"], 7, "Команда", nil, 3.0, nil)
	if team.Place != 1 {
		t.Fatalf("pinned place = %v, want 1 (scored place was 3)", team.Place)
	}
	blob.SetPin(7, nil)
	if blob.Pin(7) != nil {
		t.Fatal("cleared pin should read back nil")
	}
	if team := TeamStateFromBlob(blob.Teams["7"], 7, "Команда", nil, 3.0, nil); team.Place != 3 {
		t.Fatalf("unpinned place = %v, want the scored 3", team.Place)
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
	team := TeamStateFromBlob(section, 7, "Команда", RosterOf("Анна Б."), 2.0,
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
