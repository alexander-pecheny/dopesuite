package server

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// A buff.db with the four tables the endpoint reads, holding one release.
func writeBuffDB(t *testing.T) string {
	path := filepath.Join(t.TempDir(), "buff.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`create table releases (id integer primary key, date text, updated_at text, q real)`,
		`create table ratings (release_id integer, team_id integer, place real, rating integer, trb integer)`,
		`create table players (id integer primary key, name text, patronymic text, surname text)`,
		`create table player_ratings (release_id integer, player_id integer primary key, rating integer)`,
		`insert into releases values (7, '2026-10-08', '', 0.5), (6, '2026-10-01', '', 0.4)`,
		`insert into ratings values (7, 1, 2, 9000, 0), (7, 2, 1, 12000, 0), (6, 3, 1, 1, 0)`,
		`insert into players values (10, 'Anna', 'B', 'Petrova'), (11, 'Ivan', '', 'Ivanov')`,
		`insert into player_ratings values (7, 10, 8000), (7, 11, 0)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestRatingsServesTheMirroredRelease(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 501, "rater")
	anon := &apiClient{t: t, base: ts.URL}

	t.Setenv(buffDBEnv, "")
	mustStatus(t, c.do("GET", "/api/ratings", nil), 404)

	t.Setenv(buffDBEnv, writeBuffDB(t))
	mustStatus(t, anon.do("GET", "/api/ratings", nil), 401)
	resp := c.do("GET", "/api/ratings", nil)
	mustStatus(t, resp, 200)
	var got struct {
		ID      int64     `json:"id"`
		Q       float64   `json:"q"`
		Players [][]any   `json:"players"`
		Teams   [][]int64 `json:"teams"`
	}
	c.decode(resp, &got)
	if got.ID != 7 || got.Q != 0.5 {
		t.Fatalf("release = %d q %v, want 7 q 0.5", got.ID, got.Q)
	}
	if len(got.Players) != 1 || got.Players[0][1] != "Petrova" {
		t.Fatalf("players = %v, want only the rated Petrova", got.Players)
	}
	if len(got.Teams) != 2 || got.Teams[0][0] != 2 || got.Teams[1][0] != 1 {
		t.Fatalf("teams = %v, want release 7's, best first", got.Teams)
	}
}
