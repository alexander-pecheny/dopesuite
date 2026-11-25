package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// A minefield is a game of its own: it is created as "mp", under its own
// title, with its rules in the scheme, and it can take its tours from the
// rating.chgk.info tournament it is played as (Samarium's were 18 18 18 15 15).
func TestAMinefieldTakesItsToursFromTheRatingSite(t *testing.T) {
	t.Parallel()
	site := http.NewServeMux()
	site.HandleFunc("GET /tournaments/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "12345" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"id":12345,"questionQty":{"1":18,"2":18,"3":18,"4":15,"5":15}}`))
	})
	fake := httptest.NewServer(site)
	defer fake.Close()
	srv := newAuthTestServer(t)
	srv.Eng().RatingAPI = fake.URL
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	festID := newFest(t, db, "mines", "Фест", systemUserID(t, db))
	api := func(method, path string, body any) (int, string) {
		t.Helper()
		resp := scopedAPIRequest(t, srv, method, fmt.Sprintf("/api/fest/%d%s", festID, path), body, token)
		return resp.Code, resp.Body.String()
	}

	code, body := api(http.MethodPost, "/games", map[string]any{
		"game_type": "mp", "rating_tournament": 12345, "minus_wrong": 3, "minus_bonus": 3, "minus_empty": true})
	if code != http.StatusOK {
		t.Fatalf("create: %d %s", code, body)
	}
	var game struct {
		ID    int64  `json:"id"`
		Type  string `json:"type"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(body), &game); err != nil || game.Type != "mp" || game.Title != "Минное поле" {
		t.Fatalf("created %s", body)
	}
	code, body = api(http.MethodGet, fmt.Sprintf("/games/%d/scheme", game.ID), nil)
	var scheme struct {
		TourComp []int `json:"tourComp"`
		Minus    struct {
			Wrong, AllBonus int
			EmptyTour       bool
		} `json:"minus"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &scheme) != nil {
		t.Fatalf("scheme: %d %s", code, body)
	}
	if !slices.Equal(scheme.TourComp, []int{18, 18, 18, 15, 15}) || scheme.Minus.Wrong != 3 || scheme.Minus.AllBonus != 3 || !scheme.Minus.EmptyTour {
		t.Fatalf("scheme = %s", body)
	}

	// A tournament the site does not know is refused, and makes no game.
	if code, body := api(http.MethodPost, "/games", map[string]any{"game_type": "od", "rating_tournament": 999}); code == http.StatusOK {
		t.Fatalf("an unknown tournament made a game: %s", body)
	}
	// A plain OD's scheme has no minus block.
	code, body = api(http.MethodPost, "/games", map[string]any{"game_type": "od", "od_tours": 2, "od_questions": 6})
	if code != http.StatusOK {
		t.Fatalf("od: %d %s", code, body)
	}
	_ = json.Unmarshal([]byte(body), &game)
	_, body = api(http.MethodGet, fmt.Sprintf("/games/%d/scheme", game.ID), nil)
	var plain map[string]any
	if json.Unmarshal([]byte(body), &plain) != nil || plain["minus"] != nil {
		t.Fatalf("od scheme = %s", body)
	}
}
