package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/web/hostpages"
)

// КСИ seats the фест's roster under the фест's own numbers — a team is #13 in ОД
// and #13 here, and who did not play is marked in «Отказы». The creation form
// offers a Game its own состав only for the formats described by a scheme.
func TestFlatFormatSeatsTheWholeRoster(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	seedFestTeams(t, db, festID, 5)
	token := createTestSession(t, srv, systemUserID(t, db))

	ksiID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "ksi", "ksi_themes": "20"})
	var raw string
	if err := db.QueryRow(`select state_json from matches where game_id = ? and code = 'main'`, ksiID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var state struct {
		Participants []games.KSIParticipant `json:"participants"`
	}
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Participants) != 5 {
		t.Fatalf("в КСИ %d участников, want 5 — весь ростер феста", len(state.Participants))
	}
	for i, p := range state.Participants {
		if p.Number != i+1 {
			t.Fatalf("%s носит номер %d, а в фесте он %d — номера должны быть фестовыми", p.Name, p.Number, i+1)
		}
	}
}

// A flat format has no way to seat a chosen few, so a chosen list is refused by
// name rather than dropped on the floor.
func TestFlatFormatRefusesAChosenEntrantList(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	chosen := seedParticipants(t, db, festID, 5)

	for _, gameType := range []string{games.OD, games.KSI, games.Multi} {
		if hostpages.SeatsChosenEntrants(gameType) {
			t.Fatalf("%s не должен получать выбор состава на форме", gameType)
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		_, err = gamebuild.Create(context.Background(), tx, gamebuild.Spec{
			FestID: festID, Type: gameType, Entrants: chosen[:3],
			ODTours: 1, ODQuestions: 3, KSIThemes: 20,
			Minigames: []games.MultiGame{{Name: "Раз", Columns: []games.MultiColumn{{Values: []int{1}}}}},
		})
		tx.Rollback()
		if err == nil {
			t.Fatalf("%s молча проглотил выбранный состав", gameType)
		}
		if !strings.Contains(err.Error(), "Отказ") {
			t.Fatalf("%s: ошибка не объясняет, куда девать неигравших: %v", gameType, err)
		}
	}
}

// The formats described by a scheme keep the picker: that is what lets one фест
// hold an ЭК of 48 and a брейн of a different 48 (ADR-0009).
func TestSchemeFormatsKeepTheEntrantPicker(t *testing.T) {
	for _, gameType := range []string{games.Brain, games.SI, games.Troika, games.EK} {
		if !hostpages.SeatsChosenEntrants(gameType) {
			t.Fatalf("%s потерял выбор состава", gameType)
		}
	}
}
