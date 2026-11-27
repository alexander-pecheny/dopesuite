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

// A Тройка game has the troikas page of its own (ADR-0038): it draws under
// the game's address, its forms post there, and a troika added there is the
// fest's, as it was on the fest's page.
func TestATroikaGameKeepsItsTroikasPage(t *testing.T) {
	t.Parallel()
	srv, festID, token, createGame := troikaSeedFest(t)
	dsl := "[init]\nseed: random\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\nthemes: 6\nmetric: total\npoints: [1, 0.5, 0]\n"
	gameID := createGame(url.Values{"game_type": {"troika"}, "troika_dsl": {dsl}})
	request := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		resp := httptest.NewRecorder()
		srv.HostPageServer().HandleHostRouter(resp, req)
		return resp
	}
	page := fmt.Sprintf("/host/fest/%d/game/%d/troikas", festID, gameID)
	resp := request(http.MethodGet, page, nil)
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), fmt.Sprintf(`/game/%d/troikas"`, gameID)) {
		t.Fatalf("the game's troikas page: %d %s", resp.Code, resp.Body.String())
	}
	troikas := func() int {
		t.Helper()
		var count int
		if err := srv.Eng().DB.QueryRow(`select count(*) from participants where fest_id = ? and assembled = 1`, festID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	before := troikas()
	resp = request(http.MethodPost, page, url.Values{"mode": {"lines"}, "lines": {"Одуванчик: Ян Янов, Ум Умов\n"}})
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "Одуванчик") {
		t.Fatalf("add a troika on the game's page: %d %s", resp.Code, resp.Body.String())
	}
	if after := troikas(); after != before+1 {
		t.Fatalf("the fest has %d troikas, was %d", after, before)
	}
}
