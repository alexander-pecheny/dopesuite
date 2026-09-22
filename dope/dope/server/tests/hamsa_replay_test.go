package tests

import (
	"testing"
	"time"

	"dope/dope/domain/games"
	"dope/dope/domain/replay"
)

// Хамса, «Город героев-2026», replayed бой by бой (ADR-0010). The tournament
// is invented — it is played in October — so the transcript is not a sheet but
// a worked example: the marks, the ставки and the перестрелка are made up, and
// every Σ, место and both tables are computed from the регламент alone.
//
// It is the only place the whole formula runs end to end: the seed dealt in
// straight bands, place k of every table carried into table k, the three
// fourth places drawn by hand, the Block ranked on the сумма мест over both
// Игры with the КСИ place closing the chain, and a Финал split by an extra
// Персональный round.
//
// The письменный отбор is a КСИ Game of its own, and a transcript describes
// one Game rather than a chain of them, so the roster order here is the seed
// the отбор produced.
func TestHamsaReplay(t *testing.T) {
	script, err := replay.Parse(readFile(t, "../../../testdata/hamsa2026/hamsa.transcript"))
	if err != nil {
		t.Fatal(err)
	}
	srv := newAuthTestServer(t)
	srv.SetEditBatchWindow(time.Millisecond)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	festID := newFest(t, db, "hamsa-2026", "Город героев-2026", systemUserID(t, db))

	numbers := map[string]int{}
	names := rosterOf(script)
	for i, name := range names {
		numbers[name] = i + 1
	}
	teams := registerNumberedTeams(t, db, festID, numbers)
	createKSIGame(t, db, festID)

	gameID := createSchemeGameFor(t, db, festID, games.Hamsa, "Хамса",
		readFile(t, "../../../scripts/hamsa/"+script.Scheme), idsFor(t, teams, names))
	game := &serverGame{t: t, srv: srv, festID: festID, gameID: gameID, gameType: games.Hamsa, token: token}
	game.via = directTransport{game}

	findings, err := replay.Run(script, game)
	for _, finding := range findings {
		t.Errorf("Хамса: %s", finding)
	}
	if err != nil {
		t.Fatalf("Хамса: %v", err)
	}
	t.Logf("Хамса: %d боёв и %d таблиц сошлись", len(script.Bouts), len(script.Tables))
}

// The same day over HTTP: the handlers, the authorisation and the write path,
// including the draw endpoint a host presses between the two Игры.
func TestHamsaReplayOverHTTP(t *testing.T) {
	if testing.Short() {
		t.Skip("heavy: the HTTP Хамса replay runs without -short (just test-full)")
	}
	script, err := replay.Parse(readFile(t, "../../../testdata/hamsa2026/hamsa.transcript"))
	if err != nil {
		t.Fatal(err)
	}
	srv := newAuthTestServer(t)
	srv.SetEditBatchWindow(time.Millisecond)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	festID := newFest(t, db, "hamsa-2026-http", "Город героев-2026", systemUserID(t, db))

	numbers := map[string]int{}
	names := rosterOf(script)
	for i, name := range names {
		numbers[name] = i + 1
	}
	teams := registerNumberedTeams(t, db, festID, numbers)
	createKSIGame(t, db, festID)

	gameID := createSchemeGameFor(t, db, festID, games.Hamsa, "Хамса",
		readFile(t, "../../../scripts/hamsa/"+script.Scheme), idsFor(t, teams, names))
	game := &serverGame{t: t, srv: srv, festID: festID, gameID: gameID, gameType: games.Hamsa, token: token}
	game.via = httpTransport{game}

	findings, err := replay.Run(script, game)
	for _, finding := range findings {
		t.Errorf("Хамса по HTTP: %s", finding)
	}
	if err != nil {
		t.Fatalf("Хамса по HTTP: %v", err)
	}
}
