package tests

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"dope/dope/domain/games"
	"dope/dope/domain/replay"
	"dope/dope/platform/util"
)

// The whole championship on one фест, built the way a host would build it and
// then replayed бой by бой from the committed transcripts.
//
// This is the deliverable the harness was for: not four games proved separately,
// but one фест that holds all of them at once — 65 registered teams, an ЭК of 48
// and a брейн of a different 48, and two individual tournaments beside them.
//
// It takes minutes, so it runs on request rather than on every suite: set
// DOPE_STUDCHR_FEST to the path the finished database should be written to.
func TestStudchrWholeFest(t *testing.T) {
	out := os.Getenv("DOPE_STUDCHR_FEST")
	if out == "" {
		t.Skip("установите DOPE_STUDCHR_FEST=<путь к базе>, чтобы собрать фест")
	}
	srv := newAuthTestServer(t)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, srv.Eng().DB))
	// A фест of its own. The bootstrap фест carries a demo game and demo teams,
	// and a database meant to be published should hold the championship and
	// nothing else.
	festID := newFest(t, db, "studchr-2026", "Студенческий чемпионат России 2026",
		systemUserID(t, srv.Eng().DB))

	scripts := map[string]replay.Script{}
	for _, name := range []string{"ek", "brain", "si", "tpsh"} {
		script, err := replay.Parse(readFile(t, "../../../testdata/studchr2026/"+name+".transcript"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		scripts[name] = script
	}

	// The фест's registry. ОД seated every team the championship registered, and
	// both 48-team games are subsets of it, so its list in its own numbering is
	// the фест's registry — which is what a registry is.
	od := readOD(t)
	teams := registerParticipants(t, db, festID, "team",
		union(od.names(), rosterOf(scripts["ek"]), rosterOf(scripts["brain"])))
	players := registerParticipants(t, db, festID, "player",
		union(rosterOf(scripts["si"]), rosterOf(scripts["tpsh"])))
	t.Logf("реестр феста: %d команд, %d игроков", len(teams), len(players))

	for _, c := range []struct{ name, gameType, title, scheme string }{
		{"ek", games.EK, "ЭК", "ek.dsl"},
		{"brain", games.Brain, "Брейн", "brain.dsl"},
		{"si", games.SI, "СИ", "si.dsl"},
		{"tpsh", games.SI, "ТПШ", "tpsh.dsl"},
	} {
		script := scripts[c.name]
		registry := teams
		if games.IsIndividual(c.gameType) {
			registry = players
		}
		gameID := createSchemeGameFor(t, db, festID, c.gameType, c.title,
			readFile(t, "../../../scripts/studchr/"+c.scheme), idsFor(t, registry, rosterOf(script)))
		game := &serverGame{t: t, srv: srv, festID: festID, gameID: gameID, gameType: c.gameType, token: token}
		findings, err := replay.Run(script, game)
		for _, f := range findings {
			t.Errorf("%s: %s", c.title, f)
		}
		if err != nil {
			t.Fatalf("%s: %v", c.title, err)
		}
		t.Logf("%s: %d боёв сошлись", c.title, len(script.Bouts))
	}

	// ОД has no бои: its whole document is one grid of which teams took which
	// question, held on the game. So it is loaded rather than replayed, through
	// the same patch path the page edits it by.
	odGame := createSchemeGameFor(t, db, festID, games.OD, "ОД",
		readFile(t, "../../../scripts/studchr/od.dsl"), idsFor(t, teams, od.names()))
	// ОД's team list belongs to the rating import and the protocol declares it
	// immutable under host edits, so it is seated before any play rather than
	// patched — the same thing the import does, by the same route.
	seatODTeams(t, db, odGame, od)
	resp := scopedAPIRequest(t, srv, "PATCH", fmt.Sprintf("/api/fest/%d/games/%d/state", festID, odGame),
		map[string]any{"ops": []map[string]any{
			{"path": []any{"entries"}, "value": od.Entries},
			{"path": []any{"completed"}, "value": od.Completed},
		}}, token)
	if resp.Code != 200 {
		t.Fatalf("ОД: %d %s", resp.Code, resp.Body.String())
	}
	t.Logf("ОД: %d команд, %d вопросов", len(od.Teams), len(od.Entries))

	// The bootstrap фест is the test server's demo, not the championship.
	if _, err := db.Exec(`delete from fests where id <> ?`, festID); err != nil {
		t.Fatalf("убрать демо-фест: %v", err)
	}
	_ = os.Remove(out)
	if _, err := db.Exec(`vacuum into ?`, out); err != nil {
		t.Fatalf("выгрузить базу: %v", err)
	}
	t.Logf("фест собран: %s", out)
}

func registerParticipants(t *testing.T, db *sql.DB, festID int64, roster string, names []string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for i, name := range names {
		res, err := db.Exec(`
insert into participants(fest_id, roster, name, city, number) values(?, ?, ?, '', ?)`,
			festID, roster, name, i+1)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		out[name] = id
	}
	return out
}

func rosterOf(script replay.Script) []string {
	out := make([]string, len(script.Roster))
	for i, entrant := range script.Roster {
		out[i] = entrant.Name
	}
	return out
}

func idsFor(t *testing.T, registry map[string]int64, names []string) []int64 {
	t.Helper()
	out := make([]int64, len(names))
	for i, name := range names {
		id, ok := registry[name]
		if !ok {
			t.Fatalf("в реестре феста нет %q", name)
		}
		out[i] = id
	}
	return out
}

// odData is ОД's document as the sheet holds it: the teams in their own
// numbering, and per question the numbers of the teams that took it.
type odData struct {
	Teams []struct {
		Number int    `json:"number"`
		Name   string `json:"name"`
		City   string `json:"city"`
	} `json:"teams"`
	Entries   [][]int `json:"entries"`
	Completed []bool  `json:"completed"`
}

func (d odData) names() []string {
	out := make([]string, len(d.Teams))
	for i, team := range d.Teams {
		out[i] = team.Name
	}
	return out
}

func readOD(t *testing.T) odData {
	t.Helper()
	var data odData
	if err := json.Unmarshal([]byte(readFile(t, "../../../testdata/studchr2026/od.json")), &data); err != nil {
		t.Fatal(err)
	}
	return data
}

// seatODTeams writes the ОД game's team list, which the protocol declares
// immutable once play starts (RatingRosterStateKey), so the patch path refuses
// it and the import owns it instead.
func seatODTeams(t *testing.T, db *sql.DB, gameID int64, od odData) {
	t.Helper()
	var raw string
	if err := db.QueryRow(`select coalesce(state_json, '{}') from games where id = ?`, gameID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatal(err)
	}
	seated := make([]map[string]string, len(od.Teams))
	for i, team := range od.Teams {
		seated[i] = map[string]string{"name": team.Name, "city": team.City}
	}
	state["teams"] = seated
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`update games set state_json = ? where id = ?`, string(encoded), gameID); err != nil {
		t.Fatal(err)
	}
}

func newFest(t *testing.T, db *sql.DB, slug, title string, owner int64) int64 {
	t.Helper()
	now := util.UtcNow()
	res, err := db.Exec(`
insert into fests(slug, title, revision, created_at, updated_at) values(?, ?, 1, ?, ?)`,
		slug, title, now, now)
	if err != nil {
		t.Fatalf("создать фест: %v", err)
	}
	festID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
insert into fest_organizers(fest_id, user_id, role, added_at) values(?, ?, 'creator', ?)`,
		festID, owner, now); err != nil {
		t.Fatalf("организатор феста: %v", err)
	}
	return festID
}
