package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// A ЧГК's tours change on a Game under way: the answers keep their question
// numbers and only the tour boundaries move. Tours that would drop a question
// something is entered on are refused. The tours also load from the fest's
// tournament on the tournament site.
func TestAGamesToursChangeWithItsAnswersKept(t *testing.T) {
	t.Parallel()
	site := http.NewServeMux()
	site.HandleFunc("GET /tournaments/{id}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"questionQty":{"1":4,"2":4}}`))
	})
	fake := httptest.NewServer(site)
	defer fake.Close()
	srv := newAuthTestServer(t)
	srv.Eng().RatingAPI = fake.URL
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	festID := newFest(t, db, "retour", "Фест", systemUserID(t, db))
	seedFestTeams(t, db, festID, 2)
	api := func(method, path string, body any) (int, string) {
		t.Helper()
		resp := scopedAPIRequest(t, srv, method, fmt.Sprintf("/api/fest/%d%s", festID, path), body, token)
		return resp.Code, resp.Body.String()
	}
	create := func(gameType string, tours, questions int) int64 {
		t.Helper()
		code, body := api(http.MethodPost, "/games", map[string]any{"game_type": gameType, "od_tours": tours, "od_questions": questions})
		var game struct {
			ID int64 `json:"id"`
		}
		if code != http.StatusOK || json.Unmarshal([]byte(body), &game) != nil {
			t.Fatalf("create %s: %d %s", gameType, code, body)
		}
		return game.ID
	}
	mines := create("mp", 3, 3)
	// Team 2 answers question 5 (index 4), which is tour 2's second.
	if code, body := api(http.MethodPatch, fmt.Sprintf("/games/%d/state", mines), map[string]any{"ops": []map[string]any{
		{"op": "set", "path": []any{"entries", 4, 1}, "value": 2},
		{"op": "set", "path": []any{"completed", 4}, "value": true},
	}}); code != http.StatusOK {
		t.Fatalf("answer: %d %s", code, body)
	}
	retour := func(body map[string]any) (int, string) {
		t.Helper()
		return api(http.MethodPost, fmt.Sprintf("/games/%d/tours", mines), body)
	}
	read := func() ([]int, [][]int64, []bool) {
		t.Helper()
		_, body := api(http.MethodGet, fmt.Sprintf("/games/%d/settings", mines), nil)
		var settings struct {
			Tours []int `json:"tours"`
		}
		if err := json.Unmarshal([]byte(body), &settings); err != nil {
			t.Fatalf("settings: %s", body)
		}
		_, body = api(http.MethodGet, fmt.Sprintf("/games/%d/state", mines), nil)
		var doc struct {
			Entries   [][]int64 `json:"entries"`
			Completed []bool    `json:"completed"`
		}
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("state: %s", body)
		}
		return settings.Tours, doc.Entries, doc.Completed
	}

	if code, body := retour(map[string]any{"tours": []int{5, 5}}); code != http.StatusOK {
		t.Fatalf("5 5: %d %s", code, body)
	}
	tours, entries, completed := read()
	if !slices.Equal(tours, []int{5, 5}) || len(entries) != 10 || len(completed) != 10 || entries[4][1] != 2 || !completed[4] {
		t.Fatalf("after 5 5: tours %v, entries %v, completed %v", tours, entries, completed)
	}

	// Four questions would drop question 5, which holds an answer.
	if code, body := retour(map[string]any{"tours": []int{2, 2}}); code == http.StatusOK {
		t.Fatalf("2 2 dropped an answered question: %s", body)
	}
	if code, _ := retour(map[string]any{"tours": []int{0, 3}}); code == http.StatusOK {
		t.Fatal("a tour of no questions was taken")
	}

	if code, _ := retour(map[string]any{"from_site": true}); code == http.StatusOK {
		t.Fatal("a fest with no tournament loaded tours from the site")
	}
	if _, err := db.Exec(`update fests set rating_id = 777 where id = ?`, festID); err != nil {
		t.Fatal(err)
	}
	if code, body := retour(map[string]any{"from_site": true}); code != http.StatusOK {
		t.Fatalf("from the site: %d %s", code, body)
	}
	if tours, entries, _ = read(); !slices.Equal(tours, []int{4, 4}) || entries[4][1] != 2 {
		t.Fatalf("from the site: tours %v, entries %v", tours, entries)
	}
	// The minefield scores its tours as they are now: tour 2 holds the answer.
	_, body := api(http.MethodGet, fmt.Sprintf("/games/%d/scheme", mines), nil)
	var scheme struct {
		TourComp []int `json:"tourComp"`
	}
	if json.Unmarshal([]byte(body), &scheme) != nil || !slices.Equal(scheme.TourComp, []int{4, 4}) {
		t.Fatalf("scheme: %s", body)
	}
}
