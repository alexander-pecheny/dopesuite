package tests

import (
	"database/sql"
	"testing"
)

// The fest's venues are shared by its Games. Octobearfest's Троечка lists
// «Фойе, Актовый зал, …» and its Своячок «Актовый зал, Гримерка, …»: matched
// by number, the Своячок's first table became the Троечка's «Фойе» and its
// own titles were dropped. A titled venue is found by its title, and a new
// title takes the next free number.
func TestSchemesShareVenuesByTitle(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	seedFestTeams(t, db, festID, 4)
	first := createSchemeGame(t, db, festID, "brain", "Первая",
		"[defaults]\nquestions: 3\nvenues: [Фойе, Актовый зал]\n\n[scheme]\nkind: roundrobin\ngroup_size: 2\ngroups: 2\n")
	second := createSchemeGame(t, db, festID, "brain", "Вторая",
		"[defaults]\nquestions: 3\nvenues: [актовый  зал, Гримёрка]\n\n[scheme]\nkind: roundrobin\ngroup_size: 2\ngroups: 2\n")

	venues := map[int]string{}
	rows, err := db.Query(`select number, title from venues where fest_id = ? order by number`, festID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n int
		var title string
		if err := rows.Scan(&n, &title); err != nil {
			t.Fatal(err)
		}
		venues[n] = title
	}
	rows.Close()
	// The test fest already has venue 1; the rooms take the numbers after it,
	// and «актовый  зал» is the first game's «Актовый зал», not a fourth room.
	count := map[string]int{}
	for _, title := range venues {
		count[title]++
	}
	if len(venues) != 4 || count["Фойе"] != 1 || count["Актовый зал"] != 1 || count["Гримёрка"] != 1 || venues[4] != "Гримёрка" {
		t.Fatalf("fest venues = %v, want one each of Фойе, Актовый зал and Гримёрка after venue 1", venues)
	}
	if got := matchVenues(t, db, first); got != "Фойе Актовый зал" {
		t.Fatalf("first game's bouts at %q", got)
	}
	if got := matchVenues(t, db, second); got != "Актовый зал Гримёрка" {
		t.Fatalf("second game's bouts at %q, want its own rooms", got)
	}
}

// matchVenues is the venue title of each of a game's bouts, in their order.
func matchVenues(t *testing.T, db *sql.DB, gameID int64) string {
	t.Helper()
	rows, err := db.Query(`
select coalesce(v.title, '') from matches m
join stages s on s.id = m.stage_id
left join venues v on v.id = m.venue_id
where s.game_id = ? order by s.position, m.id`, gameID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := ""
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			t.Fatal(err)
		}
		if out != "" {
			out += " "
		}
		out += title
	}
	return out
}
