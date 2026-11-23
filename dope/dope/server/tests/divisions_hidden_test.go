package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
)

// A game says which of its teams' зачёты it shows. Every Flag is one by
// default; the host hides the ones that mean nothing in this game (ЧР in a
// КСИ), and the game's view carries the hidden ones for the page to drop.
func TestAGameHidesTheDivisionsItDoesNotRank(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	srv.SetEditBatchWindow(time.Millisecond)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	festID := newFest(t, db, "hidden", "Фест", systemUserID(t, db))
	api := func(method, path string, body any) (int, string) {
		t.Helper()
		resp := scopedAPIRequest(t, srv, method, fmt.Sprintf("/api/fest/%d%s", festID, path), body, token)
		return resp.Code, resp.Body.String()
	}
	stud := festTeamWithPlayers(t, db, festID, "Альфа", [][2]string{{"А", "Один"}})
	cr := festTeamWithPlayers(t, db, festID, "Бета", [][2]string{{"Б", "Один"}})
	festTeamFlag(t, db, stud, "Студ")
	festTeamFlag(t, db, cr, "ЧР")

	code, body := api(http.MethodPost, "/games", map[string]any{"game_type": "ksi", "ksi_themes": 2})
	if code != http.StatusOK {
		t.Fatalf("create: %d %s", code, body)
	}
	var game struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &game); err != nil {
		t.Fatal(err)
	}
	type settings struct {
		Title     string   `json:"title"`
		Divisions []string `json:"divisions"`
		Hidden    []string `json:"hidden_divisions"`
	}
	read := func() settings {
		t.Helper()
		code, body := api(http.MethodGet, fmt.Sprintf("/games/%d/settings", game.ID), nil)
		var s settings
		if code != http.StatusOK || json.Unmarshal([]byte(body), &s) != nil {
			t.Fatalf("settings: %d %s", code, body)
		}
		return s
	}
	if s := read(); !reflect.DeepEqual(s.Divisions, []string{"Студ", "ЧР"}) || len(s.Hidden) != 0 {
		t.Fatalf("a new game: %+v, want both зачёты offered and none hidden", s)
	}

	if code, body := api(http.MethodPatch, fmt.Sprintf("/games/%d/settings", game.ID), map[string]any{"hidden_divisions": []string{"ЧР"}}); code != http.StatusOK {
		t.Fatalf("hide ЧР: %d %s", code, body)
	}
	if s := read(); !reflect.DeepEqual(s.Hidden, []string{"ЧР"}) {
		t.Fatalf("after hiding: %+v", s)
	}
	// A rename leaves the зачёты as they are.
	if code, body := api(http.MethodPatch, fmt.Sprintf("/games/%d/settings", game.ID), map[string]any{"title": "КСИ отбора"}); code != http.StatusOK {
		t.Fatalf("rename: %d %s", code, body)
	}
	if s := read(); s.Title != "КСИ отбора" || !reflect.DeepEqual(s.Hidden, []string{"ЧР"}) {
		t.Fatalf("after a rename: %+v", s)
	}
	code, body = api(http.MethodGet, fmt.Sprintf("/games/%d", game.ID), nil)
	var view struct {
		Hidden []string `json:"hiddenDivisions"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &view) != nil || !reflect.DeepEqual(view.Hidden, []string{"ЧР"}) {
		t.Fatalf("the game's view: %d %s", code, body)
	}

	// The settings page: a box per offered зачёт, ticked when shown. Ticking
	// ЧР and leaving Студ unticked swaps them.
	form := url.Values{"title": {"КСИ отбора"}, "divisions_present": {"1"}, "division_shown": {"ЧР"}}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/game/%d/settings", festID, game.ID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	resp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(resp, req)
	if resp.Code != http.StatusSeeOther {
		t.Fatalf("settings form: %d %s", resp.Code, resp.Body.String())
	}
	if s := read(); !reflect.DeepEqual(s.Hidden, []string{"Студ"}) {
		t.Fatalf("after the form: %+v, want Студ hidden", s)
	}
	// The twin takes the same ticks: Студ shown again, ЧР hidden.
	if code, body := api(http.MethodPatch, fmt.Sprintf("/games/%d/settings", game.ID), map[string]any{"shown_divisions": []string{"Студ"}}); code != http.StatusOK {
		t.Fatalf("shown_divisions: %d %s", code, body)
	}
	if s := read(); !reflect.DeepEqual(s.Hidden, []string{"ЧР"}) {
		t.Fatalf("after shown_divisions: %+v, want ЧР hidden", s)
	}
	page := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d/game/%d/settings", festID, game.ID), nil)
	page.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	resp = httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(resp, page)
	if body := resp.Body.String(); !strings.Contains(body, `name="division_shown"`) || !strings.Contains(body, "Зачёты") {
		t.Fatalf("the settings page has no зачёты field: %d", resp.Code)
	}
}

// An ОД ranks a зачёт вне зачёта: coaches play the whole game, and the page
// sets them below everyone else in «Все». The game keeps the Flags through
// the API twin and the settings form, and its view carries them.
func TestAnODRanksADivisionOutside(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	srv.SetEditBatchWindow(time.Millisecond)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	festID := newFest(t, db, "outside", "Фест", systemUserID(t, db))
	api := func(method, path string, body any) (int, string) {
		t.Helper()
		resp := scopedAPIRequest(t, srv, method, fmt.Sprintf("/api/fest/%d%s", festID, path), body, token)
		return resp.Code, resp.Body.String()
	}
	school := festTeamWithPlayers(t, db, festID, "Альфа", [][2]string{{"А", "Один"}})
	coaches := festTeamWithPlayers(t, db, festID, "Тренеры", [][2]string{{"Б", "Один"}})
	festTeamFlag(t, db, school, "Ш")
	festTeamFlag(t, db, coaches, "Тр")

	code, body := api(http.MethodPost, "/games", map[string]any{"game_type": "od", "od_tours": 1, "od_questions": 3})
	if code != http.StatusOK {
		t.Fatalf("create: %d %s", code, body)
	}
	var game struct {
		ID      int64    `json:"id"`
		Outside []string `json:"outside_divisions"`
	}
	if err := json.Unmarshal([]byte(body), &game); err != nil || game.Outside == nil || len(game.Outside) != 0 {
		t.Fatalf("a new game: %s, want an empty outside_divisions", body)
	}
	read := func() []string {
		t.Helper()
		code, body := api(http.MethodGet, fmt.Sprintf("/games/%d/settings", game.ID), nil)
		var s struct {
			Outside []string `json:"outside_divisions"`
		}
		if code != http.StatusOK || json.Unmarshal([]byte(body), &s) != nil {
			t.Fatalf("settings: %d %s", code, body)
		}
		return s.Outside
	}
	if code, body := api(http.MethodPatch, fmt.Sprintf("/games/%d/settings", game.ID), map[string]any{"outside_divisions": []string{"Тр"}}); code != http.StatusOK {
		t.Fatalf("outside Тр: %d %s", code, body)
	}
	// A rename keeps them.
	if code, body := api(http.MethodPatch, fmt.Sprintf("/games/%d/settings", game.ID), map[string]any{"title": "Вопросики"}); code != http.StatusOK {
		t.Fatalf("rename: %d %s", code, body)
	}
	if got := read(); !reflect.DeepEqual(got, []string{"Тр"}) {
		t.Fatalf("after a rename: %v, want [Тр]", got)
	}
	code, body = api(http.MethodGet, fmt.Sprintf("/games/%d", game.ID), nil)
	var view struct {
		Outside []string `json:"outsideDivisions"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &view) != nil || !reflect.DeepEqual(view.Outside, []string{"Тр"}) {
		t.Fatalf("the game's view: %d %s", code, body)
	}

	host := func(req *http.Request) *httptest.ResponseRecorder {
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		resp := httptest.NewRecorder()
		srv.HostPageServer().HandleHostRouter(resp, req)
		return resp
	}
	page := host(httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d/game/%d/settings", festID, game.ID), nil))
	if body := page.Body.String(); !strings.Contains(body, `name="division_outside"`) || !strings.Contains(body, "Вне зачёта") {
		t.Fatalf("the settings page has no вне-зачёта field: %d", page.Code)
	}
	// Unticking every box clears them; the зачёты shown stay as they were.
	form := url.Values{"title": {"Вопросики"}, "divisions_present": {"1"}, "division_shown": {"Ш", "Тр"}, "outside_present": {"1"}}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/game/%d/settings", festID, game.ID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp := host(req); resp.Code != http.StatusSeeOther {
		t.Fatalf("settings form: %d %s", resp.Code, resp.Body.String())
	}
	if got := read(); len(got) != 0 {
		t.Fatalf("after the form: %v, want none", got)
	}
}
