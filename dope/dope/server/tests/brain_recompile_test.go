package tests

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"pecheny.me/dopecore/session"
)

// A DSL edit recompiles only what has not started: bumping questions rebuilds
// pristine бої (new empty state) but leaves a бой with entered marks intact,
// and a structural edit that would delete that бой is refused, naming it.
func TestBrainRecompileGuard(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	token := createTestSession(t, srv, systemUserID(t, srv.Eng().DB))

	for i, name := range []string{"Астра", "Берёза", "Вяз", "Гинкго"} {
		if _, err := srv.Eng().DB.Exec(`
insert into fest_teams(fest_id, name, city, position, number) values(?, ?, '', ?, ?)`,
			festID, name, i+1, i+1); err != nil {
			t.Fatalf("insert fest team: %v", err)
		}
	}

	post := func(path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		resp := httptest.NewRecorder()
		srv.HostPageServer().HandleHostRouter(resp, req)
		return resp
	}

	dsl5 := "[defaults]\nquestions: 5\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\n"
	if resp := post(fmt.Sprintf("/host/fest/%d/game/new", festID), url.Values{"game_type": {"brain"}, "brain_dsl": {dsl5}}); resp.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, body %s", resp.Code, resp.Body.String())
	}
	var gameID int64
	if err := srv.Eng().DB.QueryRow(`select id from games where fest_id = ? and game_type = 'brain'`, festID).Scan(&gameID); err != nil {
		t.Fatalf("brain game: %v", err)
	}

	patch := map[string]any{"ops": []map[string]any{
		{"path": []any{"teams", 0, "rows", 0, "mark"}, "value": "right"},
	}}
	if resp := scopedAPIRequest(t, srv, http.MethodPatch,
		fmt.Sprintf("/api/fest/%d/games/%d/matches/s1-g1-1/state", festID, gameID), patch, token); resp.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body %s", resp.Code, resp.Body.String())
	}

	settingsPath := fmt.Sprintf("/host/fest/%d/game/%d/settings", festID, gameID)
	dsl7 := "[defaults]\nquestions: 7\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\n"
	if resp := post(settingsPath, url.Values{"title": {"Брейн"}, "brain_dsl": {dsl7}}); resp.Code != http.StatusSeeOther {
		t.Fatalf("recompile status = %d, body %s", resp.Code, resp.Body.String())
	}

	rowCount := func(code string) int {
		t.Helper()
		var rowsLen int
		if err := srv.Eng().DB.QueryRow(`
select json_array_length(state_json -> '$.teams[0].rows') from matches where game_id = ? and code = ?`,
			gameID, code).Scan(&rowsLen); err != nil {
			t.Fatalf("state rows %s: %v", code, err)
		}
		return rowsLen
	}
	if got := rowCount("s1-g1-1"); got != 5 {
		t.Fatalf("started бой rebuilt: rows = %d, want 5 kept", got)
	}
	if got := rowCount("s1-g1-2"); got != 7 {
		t.Fatalf("pristine бой rows = %d, want 7", got)
	}
	var mark string
	if err := srv.Eng().DB.QueryRow(`
select state_json ->> '$.teams[0].rows[0].mark' from matches where game_id = ? and code = 's1-g1-1'`,
		gameID).Scan(&mark); err != nil || mark != "right" {
		t.Fatalf("started бой mark = %q err %v, want right", mark, err)
	}

	// Splitting into two groups reuses code s1-g1-1 but reseats it (seed 1 vs
	// seed 4 instead of 1 vs 2) — the started бой must block the edit.
	dslShrink := "[defaults]\nquestions: 5\n\n[scheme]\nkind: roundrobin\ngroups: 2\ngroup_size: 2\n"
	resp := post(settingsPath, url.Values{"title": {"Брейн"}, "brain_dsl": {dslShrink}})
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "s1-g1-1") {
		t.Fatalf("shrink status = %d, want error page naming s1-g1-1; body head: %.200s", resp.Code, resp.Body.String())
	}
	var matchCount int
	if err := srv.Eng().DB.QueryRow(`select count(*) from matches where game_id = ?`, gameID).Scan(&matchCount); err != nil || matchCount != 6 {
		t.Fatalf("matches after refused edit = %d, want 6 untouched", matchCount)
	}
}
