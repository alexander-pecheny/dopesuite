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

	"pecheny.me/dopecore/session"
)

// A game says which of its teams' зачёты it shows. Every Flag is one by
// default; the host hides the ones that mean nothing in this game (ЧР in a
// КСИ), and the game's view carries the hidden ones for the page to drop.
func TestAGameHidesTheDivisionsItDoesNotRank(t *testing.T) {
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
	page := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d/game/%d/settings", festID, game.ID), nil)
	page.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	resp = httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(resp, page)
	if body := resp.Body.String(); !strings.Contains(body, `name="division_shown"`) || !strings.Contains(body, "Зачёты") {
		t.Fatalf("the settings page has no зачёты field: %d", resp.Code)
	}
}
