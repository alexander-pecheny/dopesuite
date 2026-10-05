package tests

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"dope/dope/domain/fixture"
	"dope/dope/domain/games"
)

// Every format built from a scheme takes a DSL edit on its settings page, and
// a recompile over a Game that has been played keeps every bout's document:
// the fixture fest plays one Game of each format to the end, and editing each
// one's scheme changes none of what was entered.
func TestEveryDSLGameTakesAnEditAndKeepsItsResults(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	db := srv.Eng().DB
	owner := systemUserID(t, db)
	festID, err := fixture.Build(context.Background(), db, fixture.Options{Slug: "dsl-edit", Owner: owner})
	if err != nil {
		t.Fatal(err)
	}
	token := createTestSession(t, srv, owner)
	documents := func(gameID int64) map[string]string {
		t.Helper()
		out := map[string]string{}
		rows, err := db.Query(`select code, coalesce(state_json, '') from matches where game_id = ?`, gameID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var code, state string
			if err := rows.Scan(&code, &state); err != nil {
				t.Fatal(err)
			}
			out[code] = state
		}
		return out
	}
	rows, err := db.Query(`select id, game_type, coalesce(scheme_dsl, '') from games where fest_id = ? order by position`, festID)
	if err != nil {
		t.Fatal(err)
	}
	type game struct {
		id   int64
		kind string
		dsl  string
	}
	var found []game
	for rows.Next() {
		var g game
		if err := rows.Scan(&g.id, &g.kind, &g.dsl); err != nil {
			t.Fatal(err)
		}
		found = append(found, g)
	}
	rows.Close()
	edited := 0
	for _, g := range found {
		if games.Get(g.kind).DSL != games.DSLEditable || g.dsl == "" {
			continue
		}
		before := documents(g.id)
		path := fmt.Sprintf("/api/fest/%d/games/%d/settings", festID, g.id)
		resp := scopedAPIRequest(t, srv, http.MethodPatch, path, map[string]any{"scheme_dsl": g.dsl + "\n# правка\n"}, token)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s: settings answered %d: %s", g.kind, resp.Code, resp.Body.String())
		}
		after := documents(g.id)
		for code, state := range before {
			if after[code] != state {
				t.Fatalf("%s: bout %s changed under a recompile:\n%s\n→\n%s", g.kind, code, state, after[code])
			}
		}
		edited++
	}
	if edited < 5 {
		t.Fatalf("edited %d Games; the fixture has brain, ЭК, личная СИ, Тройка and Хамса", edited)
	}
}
