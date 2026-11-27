package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
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

// The settings page has one form and one Сохранить: the minigames sent as
// they are leave the shape alone and the new title and slug are saved; a
// changed shape the game refuses keeps everything unsaved and the page shows
// what was typed.
func TestAMultiGamesSettingsSaveTitleAndMinigamesTogether(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedFestTeams(t, db, festID, 2)
	multiID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "multi", "multi_games": "Морской бой: {0-30}"})
	post := func(fields url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/game/%d/settings", festID, multiID), strings.NewReader(fields.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		resp := httptest.NewRecorder()
		srv.HostPageServer().HandleHostRouter(resp, req)
		return resp
	}
	stored := func() (title, slug, scheme string) {
		t.Helper()
		if err := db.QueryRow(`select title, coalesce(slug, ''), scheme_json from games where id = ?`, multiID).Scan(&title, &slug, &scheme); err != nil {
			t.Fatal(err)
		}
		return
	}
	_, _, before := stored()
	resp := post(url.Values{"title": {"Медиаигры"}, "slug": {"media"}, "multi_games": {"Морской бой: {0-30}\r\n"}, "multi_sorting": {""}})
	if resp.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", resp.Code, resp.Body.String())
	}
	if title, slug, scheme := stored(); title != "Медиаигры" || slug != "media" || scheme != before {
		t.Fatalf("stored %q %q, scheme changed %v", title, slug, scheme != before)
	}

	patch := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, multiID), map[string]any{"ops": []map[string]any{
		{"path": []any{"games", 0, "cells", 0, 0}, "value": 25},
	}}, token)
	if patch.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", patch.Code, patch.Body.String())
	}
	resp = post(url.Values{"title": {"Медийки"}, "slug": {"media2"}, "multi_games": {"Морской бой: {0-20}"}, "multi_sorting": {""}})
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `value="Медийки"`) || !strings.Contains(resp.Body.String(), "{0-20}") {
		t.Fatalf("a refused save: %d %s", resp.Code, resp.Body.String())
	}
	if n := strings.Count(resp.Body.String(), `type="submit"`); n != 1 {
		t.Fatalf("the settings page has %d buttons, want one Сохранить", n)
	}
	if title, slug, _ := stored(); title != "Медиаигры" || slug != "media" {
		t.Fatalf("a refused save stored %q %q", title, slug)
	}
}
