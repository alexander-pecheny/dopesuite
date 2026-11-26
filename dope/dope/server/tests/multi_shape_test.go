package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"dope/dope/domain/imports"
	"dope/dope/domain/roster"
)

// A Мультиигры game's minigames are written again on a Game already under
// way, as the Воронёнок's media games were: the settings answer the spec as
// the creation form took it, a new one keeps what was entered in a minigame
// under its name, and one that would lose an entered value is refused.
func TestAMultiGamesMinigamesChangeWithItsCellsKept(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	teams := []roster.FestRosterImportTeam{{RatingID: 2, Name: "Альфа"}, {RatingID: 3, Name: "Бета"}}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, teams, imports.RosterChoice{}); err != nil {
		t.Fatalf("import: %v", err)
	}
	multiID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "multi",
		"multi_games": "Морской бой →0..100: {0-30}\nКатакана →0..100: {0-100}\nМедиа-Эрудит (вне итога): {0-1000}", "multi_sorting": "total, game3"})
	base := fmt.Sprintf("/api/fest/%d/games/%d", festID, multiID)
	api := func(method, path string, body any) (int, string) {
		t.Helper()
		resp := scopedAPIRequest(t, srv, method, base+path, body, token)
		return resp.Code, resp.Body.String()
	}
	shape := func() (s struct{ Spec, Sorting string }) {
		t.Helper()
		_, body := api(http.MethodGet, "/settings", nil)
		var settings struct {
			Shape struct {
				Spec    string `json:"spec"`
				Sorting string `json:"sorting"`
			} `json:"shape"`
		}
		if err := json.Unmarshal([]byte(body), &settings); err != nil {
			t.Fatalf("settings: %s", body)
		}
		return struct{ Spec, Sorting string }{settings.Shape.Spec, settings.Shape.Sorting}
	}
	if got := shape(); got.Spec != "Морской бой ->0..100: {0-30}\nКатакана ->0..100: {0-100}\nМедиа-Эрудит (вне итога): {0-1000}" || got.Sorting != "total, game3" {
		t.Fatalf("shape = %+v", got)
	}
	if code, body := api(http.MethodPatch, "/state", map[string]any{"ops": []map[string]any{
		{"path": []any{"games", 0, "cells", 1, 0}, "value": 17},
	}}); code != http.StatusOK {
		t.Fatalf("patch: %d %s", code, body)
	}

	// Морской бой holds a 17 in its one task, which a split into Ячейки and
	// Корабли keeps under Ячейки, but which {0-10} would not take.
	if code, body := api(http.MethodPost, "/shape", map[string]any{"spec": "Морской бой →0..100: Ячейки{0-10} Корабли{0-10}", "sorting": "total"}); code != http.StatusBadRequest {
		t.Fatalf("a shape that loses the 17: %d %s", code, body)
	}
	spec := "Морской бой →0..100: Ячейки{0-20} Корабли{0-10}\nКатакана →0..100: {0-3}x20"
	if code, body := api(http.MethodPost, "/shape", map[string]any{"spec": spec, "sorting": "total"}); code != http.StatusOK {
		t.Fatalf("reshape: %d %s", code, body)
	}
	if got := shape(); got.Spec != strings.ReplaceAll(spec, "→", "->") || got.Sorting != "total" {
		t.Fatalf("shape after = %+v", got)
	}
	_, body := api(http.MethodGet, "/state", nil)
	var doc struct {
		Games []struct {
			Cells [][]int `json:"cells"`
		} `json:"games"`
		Shape string `json:"shape"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Games) != 2 || doc.Shape == "" || doc.Games[0].Cells[1][0] != 17 || len(doc.Games[0].Cells[1]) != 2 {
		t.Fatalf("state after = %s", body)
	}
	// The new tasks take entries at once.
	if code, body := api(http.MethodPatch, "/state", map[string]any{"ops": []map[string]any{
		{"path": []any{"games", 1, "cells", 0, 19}, "value": 3},
	}}); code != http.StatusOK {
		t.Fatalf("a Катакана matrix: %d %s", code, body)
	}
}
