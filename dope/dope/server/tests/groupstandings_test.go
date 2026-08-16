package tests

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A Group's Сетка is a table of place against team, not a card per бой. The
// sheets draw it that way and so should we: a группа of nine plays twelve бои,
// and twelve boxes say less about who is winning than nine rows do.
//
// The standings are already computed and already stored — stage_standings, the
// same table a пересев reads. Only the view never carried them.
func TestGroupStageCarriesItsStandings(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedFestPlayers(t, db, festID, 9)

	gameID := createSchemeGame(t, db, festID, "si", "Личная СИ",
		"[scheme]\ntype: roundrobin\nteams_in_group: 9\nmatch_size: 3\nthemes: 2\n"+
			"bout.points: seats + 1 - place\nsorting: [points, total]\n")

	code := firstMatchCode(t, db, gameID)
	patchSIMarks(t, srv, festID, gameID, code, token)
	if resp := scopedAPIRequest(t, srv, http.MethodPost,
		"/api/fest/"+itoa(festID)+"/games/"+itoa(gameID)+"/matches/"+code+"/finish",
		map[string]any{"finished": true}, token); resp.Code != http.StatusOK {
		t.Fatalf("закрыть бой: %d %s", resp.Code, resp.Body.String())
	}

	resp := scopedAPIRequest(t, srv, http.MethodGet,
		"/api/fest/"+itoa(festID)+"/games/"+itoa(gameID), nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("вид игры: %d %s", resp.Code, resp.Body.String())
	}
	var view struct {
		Stages []struct {
			Code      string `json:"code"`
			Standings []struct {
				Rank int    `json:"rank"`
				Name string `json:"name"`
			} `json:"standings"`
			Matches []struct{} `json:"matches"`
		} `json:"stages"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Stages) != 1 {
		t.Fatalf("этапов = %d, want 1", len(view.Stages))
	}
	stage := view.Stages[0]
	if len(stage.Standings) != 9 {
		t.Fatalf("в таблице группы %d строк, want 9", len(stage.Standings))
	}
	if stage.Standings[0].Rank != 1 || stage.Standings[0].Name == "" {
		t.Errorf("первая строка = %+v", stage.Standings[0])
	}
	// The бои stay: the detailed tab reads them, the Сетка does not.
	if len(stage.Matches) != 12 {
		t.Errorf("боёв = %d, want 12", len(stage.Matches))
	}
}

// A бой draws as many theme columns as it plays. Twelve is ЭК's number and used
// to be everyone's: личная СИ's группы play six, its play-off eight, its grand
// final twelve, and padding them all to twelve drew empty columns nobody could
// fill and hid the шапка the sheet prints.
func TestThemeCountFollowsTheStage(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedFestPlayers(t, db, festID, 4)

	gameID := createSchemeGame(t, db, festID, "si", "Личная СИ",
		"[scheme]\ntype: roundrobin\nteams_in_group: 4\nthemes: 6\nproceeding_teams: 2\n---\n"+
			"type: single_elimination\nteams: 2\nthemes: 9\nreseed: true\n")

	for _, c := range []struct {
		stage string
		want  int
	}{{"s1-g1", 6}, {"s2-final", 9}} {
		resp := scopedAPIRequest(t, srv, http.MethodGet,
			"/api/fest/"+itoa(festID)+"/games/"+itoa(gameID)+"/stages/"+c.stage+"/matches", nil, token)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", c.stage, resp.Code, resp.Body.String())
		}
		var matches []struct {
			Participants []struct {
				Themes []struct{} `json:"themes"`
			} `json:"participants"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &matches); err != nil {
			t.Fatal(err)
		}
		if len(matches) == 0 || len(matches[0].Participants) == 0 {
			t.Fatalf("%s: пусто", c.stage)
		}
		if got := len(matches[0].Participants[0].Themes); got != c.want {
			t.Errorf("%s: тем в бою = %d, want %d", c.stage, got, c.want)
		}
	}
}
