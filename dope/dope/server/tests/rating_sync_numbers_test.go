package tests

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// The sync from the rating site numbers the teams it brings (ADR-0038): a
// regular fest's teams without a number take the next ones free, in roster
// order, and a team that has a number keeps it through every sync.
func TestARatingSyncNumbersNewTeams(t *testing.T) {
	t.Parallel()
	var teams atomic.Value
	teams.Store([]string{"Альфа", "Бета", "Гамма"})
	site := http.NewServeMux()
	site.HandleFunc("GET /tournaments/{id}/results", func(w http.ResponseWriter, _ *http.Request) {
		var rows []string
		for i, name := range teams.Load().([]string) {
			rows = append(rows, fmt.Sprintf(`{"team":{"id":%d,"name":%q},"current":{"id":%d,"name":%q},"teamMembers":[]}`, 100+i, name, 100+i, name))
		}
		_, _ = w.Write([]byte("[" + strings.Join(rows, ",") + "]"))
	})
	fake := httptest.NewServer(site)
	defer fake.Close()
	srv := newAuthTestServer(t)
	srv.Eng().RatingAPI = fake.URL
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	festID := newFest(t, db, "sync-numbers", "Фест", systemUserID(t, db))
	if _, err := db.Exec(`update fests set rating_id = 777 where id = ?`, festID); err != nil {
		t.Fatal(err)
	}
	sync := func() {
		t.Helper()
		resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/rating-import", festID), nil, token)
		if resp.Code != http.StatusOK {
			t.Fatalf("sync: %d %s", resp.Code, resp.Body.String())
		}
	}
	numbers := func() map[string]int {
		t.Helper()
		rows, err := db.Query(`select name, coalesce(number, 0) from fest_teams where fest_id = ? and deleted = 0`, festID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]int{}
		for rows.Next() {
			var name string
			var number int
			if err := rows.Scan(&name, &number); err != nil {
				t.Fatal(err)
			}
			out[name] = number
		}
		return out
	}

	sync()
	if got := numbers(); got["Альфа"] != 1 || got["Бета"] != 2 || got["Гамма"] != 3 {
		t.Fatalf("after the first sync: %v", got)
	}
	// The host gives Бета 10; a new team on the site takes the number after
	// the highest, and nobody else's number moves.
	if _, err := db.Exec(`update fest_teams set number = 10 where fest_id = ? and name = 'Бета'`, festID); err != nil {
		t.Fatal(err)
	}
	teams.Store([]string{"Альфа", "Бета", "Гамма", "Дельта"})
	sync()
	if got := numbers(); got["Альфа"] != 1 || got["Бета"] != 10 || got["Гамма"] != 3 || got["Дельта"] != 11 {
		t.Fatalf("after the second sync: %v", got)
	}
}
