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

// A fest's first личная СИ could not pick its players: the picker offered
// Participants only, and a person became one only once some individual Game
// had seated the whole rating roster. Rating players are offered as
// "fp<id>" now, and creating the Game mints them, in the order ticked.
func TestIndividualGamePicksRatingPlayers(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	token := createTestSession(t, srv, systemUserID(t, srv.Eng().DB))
	db := srv.Eng().DB
	var ids []int64
	for _, name := range [][2]string{{"Анна", "Абрамова"}, {"Борис", "Бобров"}, {"Вера", "Волкова"}, {"Глеб", "Гусев"}} {
		res, err := db.Exec(`insert into fest_players(fest_id, first_name, last_name) values(?, ?, ?)`, festID, name[0], name[1])
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		ids = append(ids, id)
	}
	get := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d/game/new", festID), nil)
	get.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	page := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(page, get)
	if !strings.Contains(page.Body.String(), fmt.Sprintf(`value="fp%d"`, ids[2])) {
		t.Fatalf("the picker does not offer rating player fp%d", ids[2])
	}

	create := func(refs ...string) int64 {
		t.Helper()
		form := url.Values{"game_type": {"si"}, "si_dsl": {"[scheme]\nkind: flat\nthemes: 1\n"}, "entrant_id": refs}
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/game/new", festID), strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		resp := httptest.NewRecorder()
		srv.HostPageServer().HandleHostRouter(resp, req)
		if resp.Code != http.StatusSeeOther {
			t.Fatalf("create = %d, body %s", resp.Code, resp.Body.String())
		}
		var id int64
		if err := db.QueryRow(`select max(id) from games where fest_id = ?`, festID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	seated := func(gameID int64) string {
		t.Helper()
		rows, err := db.Query(`
select p.name from game_assignments ga join participants p on p.id = ga.participant_id
where ga.game_id = ? order by ga.number`, gameID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var names []string
		for rows.Next() {
			var name string
			rows.Scan(&name)
			names = append(names, name)
		}
		return strings.Join(names, ", ")
	}
	first := create(fmt.Sprintf("fp%d", ids[2]), fmt.Sprintf("fp%d", ids[0]), fmt.Sprintf("fp%d", ids[3]))
	if got := seated(first); got != "Вера Волкова, Анна Абрамова, Глеб Гусев" {
		t.Fatalf("seated %q, want the three in the order ticked", got)
	}
	var veraID int64
	if err := db.QueryRow(`select id from participants where fest_id = ? and roster = 'player' and fest_player_id = ?`, festID, ids[2]).Scan(&veraID); err != nil {
		t.Fatal(err)
	}
	// A second Game ticking the same person seats the same Participant.
	second := create(fmt.Sprint(veraID), fmt.Sprintf("fp%d", ids[1]), fmt.Sprintf("fp%d", ids[0]))
	if got := seated(second); got != "Вера Волкова, Борис Бобров, Анна Абрамова" {
		t.Fatalf("second game seated %q", got)
	}
	var people int
	if err := db.QueryRow(`select count(*) from participants where fest_id = ? and roster = 'player'`, festID).Scan(&people); err != nil {
		t.Fatal(err)
	}
	if people != 4 {
		t.Fatalf("%d player Participants, want 4 — one per person, never two", people)
	}
}
