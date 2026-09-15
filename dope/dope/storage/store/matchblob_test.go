package store

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every ЭК row written before Эрудит-Секстет names one player under `player`;
// the loader reads it as a seating of one, so old rows, golden fixtures and
// the studchr replay keep working untouched. A write emits `players` — the one
// shape — and the legacy key does not survive a round trip.
func TestBlobReadsLegacySinglePlayer(t *testing.T) {
	blob, err := ParseMatchBlob(`{"participants":{"7":{
		"themes":[{"player":55,"answers":["right","","","",""]}],
		"shootoutThemes":[{"player":56,"answers":["","","","",""]}]}}}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	section := blob.Participants["7"]
	if got := section.Themes[0].Players; len(got) != 1 || got[0] != 55 {
		t.Fatalf("themes seating = %v, want [55]", got)
	}
	if got := section.ShootoutThemes[0].Players; len(got) != 1 || got[0] != 56 {
		t.Fatalf("shootout seating = %v, want [56]", got)
	}
	raw, err := blob.JSON()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(raw, `"players":[55]`) || strings.Contains(raw, `"player":`) {
		t.Fatalf("re-encoded = %s", raw)
	}
	// A cleared seat was written as `player: 0` before; it reads as nobody,
	// not as a player whose id is zero.
	cleared, err := ParseMatchBlob(`{"participants":{"7":{"themes":[{"player":0,"answers":["","","","",""]}]}}}`)
	if err != nil {
		t.Fatalf("parse cleared: %v", err)
	}
	if got := cleared.Participants["7"].Themes[0].Players; len(got) != 0 {
		t.Fatalf("cleared seating = %v", got)
	}
}

// The blob keys team sections by team id so reseeds can never reshuffle state
// and journal patches address a stable path. Sections appear on first touch.
func TestMatchBlobRoundTrip(t *testing.T) {
	blob := MatchBlob{}
	blob.SetAnswer(17, "regular", 2, 4, "right")
	blob.SetAnswer(17, "regular", 2, 4, "right") // idempotent
	blob.SetPlayers(17, "regular", 0, []int64{55})

	raw, err := json.Marshal(blob)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back MatchBlob
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	section := back.Participants["17"]
	if section == nil {
		t.Fatalf("team 17 section missing: %s", raw)
	}
	if len(section.Themes) != 3 {
		t.Fatalf("themes grew to %d, want 3 (index 2 touched)", len(section.Themes))
	}
	if section.Themes[2].Answers[4] != "right" || len(section.Themes[0].Players) != 1 || section.Themes[0].Players[0] != 55 {
		t.Fatalf("section = %+v", section)
	}
	if back.Participant(99) == nil || len(back.Participants["99"].Themes) != 0 {
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
	if len(blob.Participants["1"].ShootoutThemes) != 1 || len(blob.Participants["2"].ShootoutThemes) != 1 {
		t.Fatalf("shootout counts after remove: %d/%d, want 1/1",
			len(blob.Participants["1"].ShootoutThemes), len(blob.Participants["2"].ShootoutThemes))
	}
	blob.RemoveTheme(1, "shootout", 5) // out of range no-ops
	if len(blob.Participants["1"].ShootoutThemes) != 1 {
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
	team := ParticipantStateFromBlob(blob.Participants["7"], 7, "Команда", nil, 3.0, nil)
	if team.Place != 1 {
		t.Fatalf("pinned place = %v, want 1 (scored place was 3)", team.Place)
	}
	blob.SetPin(7, nil)
	if blob.Pin(7) != nil {
		t.Fatal("cleared pin should read back nil")
	}
	if team := ParticipantStateFromBlob(blob.Participants["7"], 7, "Команда", nil, 3.0, nil); team.Place != 3 {
		t.Fatalf("unpinned place = %v, want the scored 3", team.Place)
	}
}

// ParticipantStateFromBlob projects a blob section into the legacy ParticipantState shape:
// theme players resolve id → display name, the grid pads to ThemeCount, and
// marks normalise — so BuildView and every downstream consumer are unchanged.
func TestParticipantStateFromBlob(t *testing.T) {
	blob := MatchBlob{}
	blob.SetPlayers(7, "regular", 1, []int64{55, 56})
	blob.SetAnswer(7, "regular", 1, 0, "+")
	blob.SetAnswer(7, "shootout", 0, 2, "Q")
	section := blob.Participants["7"]

	names := map[int64]string{55: "Анна Б.", 56: "Борис В."}
	team := ParticipantStateFromBlob(section, 7, "Команда", RosterOf("Анна Б.", "Борис В."), 2.0,
		func(id int64) string { return names[id] })
	if team.Name != "Команда" || team.Place != 2.0 || len(team.Roster) != 2 {
		t.Fatalf("identity fields: %+v", team)
	}
	if len(team.Themes) != ThemeCount {
		t.Fatalf("themes padded to %d, want %d", len(team.Themes), ThemeCount)
	}
	if len(team.Themes[1].Players) != 2 || team.Themes[1].Players[0] != "Анна Б." || team.Themes[1].Players[1] != "Борис В." || team.Themes[1].Answers[0] != "right" {
		t.Fatalf("theme 1 = %+v", team.Themes[1])
	}
	if len(team.ShootoutThemes) != 1 || team.ShootoutThemes[0].Answers[2] != "right" {
		t.Fatalf("shootout = %+v", team.ShootoutThemes)
	}
}
