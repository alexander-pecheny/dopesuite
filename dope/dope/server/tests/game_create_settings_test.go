package tests

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
)

// The creation form takes what the settings page edits, so a Game is made
// with its own title, slug and options at once rather than renamed after: a
// brain named and slugged; an ОД untitled, so named after its format, with
// one Division hidden. The JSON twin takes the title and slug too.
func TestAGameIsCreatedWithItsSettings(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedFestTeams(t, db, festID, 4)
	if _, err := db.Exec(`
insert into fest_team_flags(team_id, short, full, position)
select id, case when number % 2 = 0 then 'М' else 'Ш' end, '', 1 from fest_teams where fest_id = ?`, festID); err != nil {
		t.Fatal(err)
	}
	page := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d/game/new", festID), nil)
	page.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	pageResp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(pageResp, page)
	body := pageResp.Body.String()
	for _, want := range []string{`<select class="input" name="game_type">`, `name="title"`, `name="slug"`, `name="division_shown" value="Ш"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("the creation form lacks %s: %s", want, body)
		}
	}

	post := func(values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/game/new", festID), strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		resp := httptest.NewRecorder()
		srv.HostPageServer().HandleHostRouter(resp, req)
		return resp
	}
	type stored struct {
		title, slug, hidden string
	}
	last := func() (s stored) {
		t.Helper()
		if err := db.QueryRow(`select title, coalesce(slug, ''), coalesce(hidden_divisions, '') from games where fest_id = ? order by id desc limit 1`, festID).
			Scan(&s.title, &s.slug, &s.hidden); err != nil {
			t.Fatal(err)
		}
		return s
	}
	resp := post(url.Values{"game_type": {"brain"}, "brain_dsl": {"[defaults]\nquestions: 5\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\n"},
		"title": {"Вопросики быстрые. М"}, "slug": {"fast-m"}})
	if resp.Code != http.StatusSeeOther || !strings.HasSuffix(resp.Header().Get("Location"), "/game/fast-m/") {
		t.Fatalf("create: %d %s %s", resp.Code, resp.Header().Get("Location"), resp.Body.String())
	}
	if got := last(); got.title != "Вопросики быстрые. М" || got.slug != "fast-m" {
		t.Fatalf("stored %+v", got)
	}

	// A slug another Game holds refuses the whole creation, and the form
	// comes back as typed.
	resp = post(url.Values{"game_type": {"od"}, "od_tours": {"2"}, "od_questions": {"3"}, "title": {"ЧГК"}, "slug": {"fast-m"}})
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `value="ЧГК"`) || !strings.Contains(resp.Body.String(), `<option value="od" selected>`) {
		t.Fatalf("a taken slug: %d %s", resp.Code, resp.Body.String())
	}
	var games int
	if err := db.QueryRow(`select count(*) from games where fest_id = ? and title = 'ЧГК'`, festID).Scan(&games); err != nil || games != 0 {
		t.Fatalf("a refused creation made %d games (%v)", games, err)
	}

	resp = post(url.Values{"game_type": {"od"}, "od_tours": {"2"}, "od_questions": {"3"}, "divisions_present": {"1"}, "division_shown": {"М"}})
	if resp.Code != http.StatusSeeOther {
		t.Fatalf("create untitled: %d %s", resp.Code, resp.Body.String())
	}
	if got := last(); got.title != "ОД" || got.slug != "" || !strings.Contains(got.hidden, "Ш") || strings.Contains(got.hidden, "М") {
		t.Fatalf("an untitled ОД is stored as %+v", got)
	}

	api := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games", festID),
		map[string]any{"game_type": "od", "od_tours": 2, "od_questions": 3, "title": "Вопросики", "slug": "voprosiki"}, token)
	if api.Code != http.StatusOK {
		t.Fatalf("api create: %d %s", api.Code, api.Body.String())
	}
	if got := last(); got.title != "Вопросики" || got.slug != "voprosiki" {
		t.Fatalf("the API's game is stored as %+v", got)
	}
}
