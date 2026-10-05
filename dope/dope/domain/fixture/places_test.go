package fixture_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"dope/dope/domain/core"
	"dope/dope/domain/fixture"
	"dope/dope/domain/games"
	dopeserver "dope/dope/server"
	"dope/dope/storage/store"
)

var updatePlaces = flag.Bool("update-places", false, "rewrite web/jstest/testdata/places.json from the server's scorers")

// placesFile is the contract between the server's ranking and the pages'
// optimistic copies of it (ADR-0011, ADR-0028 §6). The pages compute a place
// the moment a host types, before the server answers, so they keep a mirror of
// each Protocol's ranking. This file holds every document of the fixture fest
// with the places the server's scorer deals; jstest/places.test.js checks each
// mirror deals the same.
const placesFile = "../../web/jstest/testdata/places.json"

// placesCase is one document as both sides read it: the Game's format and
// scheme, the document, who sits in each slot, and the places the server
// deals, in slot order.
type placesCase struct {
	Format string          `json:"format"`
	Bout   string          `json:"bout"`
	Scheme json.RawMessage `json:"scheme"`
	State  json.RawMessage `json:"state"`
	Seats  []int64         `json:"seats"`
	Places []float64       `json:"places"`
	// Themes, Totals and Plus are an EK-family bout's: how many themes it
	// plays, and each seat's Σ and Σ+ as store.ScoreParticipant reckons them.
	// The page scores the document itself while a write is in flight.
	Themes int   `json:"themes,omitempty"`
	Totals []int `json:"totals,omitempty"`
	Plus   []int `json:"plus,omitempty"`
}

// mirrored is the formats whose page ranks a document itself.
var mirrored = map[string]bool{"od": true, "ksi": true, "multi": true, "hamsa": true}

// scored is the EK family: its page draws the server's places but reckons the
// sheet's totals itself.
var scored = map[string]bool{"ek": true, "es": true, "si": true}

func TestThePagesRankAsTheServerDoes(t *testing.T) {
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	eng := &core.Engine{DB: db}
	var owner int64
	if err := eng.WithWriteTx(ctx, 0, "test", func(ctx context.Context, tx *sql.Tx) (err error) {
		owner, err = dopeserver.EnsureSystemUser(ctx, tx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	festID, err := fixture.Build(ctx, db, fixture.Options{Slug: "fixture", Owner: owner})
	if err != nil {
		t.Fatal(err)
	}
	type boutRow struct {
		format, code, scheme, state string
		matchID                     int64
	}
	bouts, err := store.CollectRows(ctx, db, `
select g.game_type, m.code, coalesce(g.scheme_json, '{}'), coalesce(m.state_json, '{}'), m.id
from matches m join games g on g.id = m.game_id
where g.fest_id = ? order by g.position, m.position, m.id`, []any{festID}, func(rows *sql.Rows) (boutRow, error) {
		var r boutRow
		return r, rows.Scan(&r.format, &r.code, &r.scheme, &r.state, &r.matchID)
	})
	if err != nil {
		t.Fatal(err)
	}
	var cases []placesCase
	for _, b := range bouts {
		if scored[b.format] {
			c, err := scoredCase(ctx, db, b.format, b.matchID)
			if err != nil {
				t.Fatalf("%s %s: %v", b.format, b.code, err)
			}
			cases = append(cases, c)
			continue
		}
		if !mirrored[b.format] {
			continue
		}
		seats, err := store.CollectRows(ctx, db, `
select coalesce(participant_id, 0) from match_slots where match_id = ? order by slot_index`, []any{b.matchID},
			func(rows *sql.Rows) (int64, error) {
				var id int64
				return id, rows.Scan(&id)
			})
		if err != nil {
			t.Fatal(err)
		}
		p, _ := games.ProtocolOf(b.format)
		outcomes, err := games.ScoreSeats(p, json.RawMessage(b.scheme), json.RawMessage(b.state), seats)
		if err != nil {
			t.Fatalf("%s %s: %v", b.format, b.code, err)
		}
		c := placesCase{Format: b.format, Bout: b.code, Scheme: json.RawMessage(b.scheme), State: json.RawMessage(b.state), Seats: seats}
		for _, o := range outcomes {
			c.Places = append(c.Places, o.Place)
		}
		cases = append(cases, c)
	}
	if len(cases) == 0 {
		t.Fatal("the fixture fest has no document a page ranks")
	}
	if *updatePlaces {
		data, err := json.MarshalIndent(cases, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(placesFile, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(placesFile)
	if err != nil {
		t.Fatalf("%v: run go test ./domain/fixture -update-places", err)
	}
	var stored []placesCase
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != len(cases) {
		t.Fatalf("places.json has %d documents, the fixture %d: run go test ./domain/fixture -update-places", len(stored), len(cases))
	}
	for i := range cases {
		if !reflect.DeepEqual(stored[i].Places, cases[i].Places) || stored[i].Bout != cases[i].Bout ||
			!reflect.DeepEqual(stored[i].Totals, cases[i].Totals) || !reflect.DeepEqual(stored[i].Plus, cases[i].Plus) {
			t.Fatalf("%s %s: places.json says %v, the server deals %v: run go test ./domain/fixture -update-places and check the pages agree",
				cases[i].Format, cases[i].Bout, stored[i].Places, cases[i].Places)
		}
	}
}

// scoredCase is an EK-family bout with the totals the server reckons for it.
func scoredCase(ctx context.Context, db *sql.DB, format string, matchID int64) (placesCase, error) {
	matches, err := store.LoadMatchStates(ctx, db, store.MatchSelector{MatchID: matchID})
	if err != nil || len(matches) != 1 {
		return placesCase{}, fmt.Errorf("load bout %d: %v", matchID, err)
	}
	m := matches[0]
	c := placesCase{Format: format, Bout: m.Code, Scheme: json.RawMessage("{}"), State: json.RawMessage(store.NonEmptyJSON(m.RawState)),
		Seats: m.ParticipantIDs, Themes: m.Themes}
	if c.Themes == 0 {
		c.Themes = store.ThemeCount
	}
	for _, team := range m.State.Participants {
		view := store.ScoreParticipant(team)
		c.Places = append(c.Places, team.Place)
		c.Totals = append(c.Totals, view.Total)
		c.Plus = append(c.Plus, view.Plus)
	}
	return c, nil
}
