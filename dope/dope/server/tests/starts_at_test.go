package tests

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"dope/dope/domain/games"
)

// A bout's start time (Bug Major 2026 asked for it): the host types it on
// one bout or on its whole wave, the bouts of one block, round and wave, and
// the bout's views carry it. A bout without one carries nothing.
func TestBoutStartTimeOnABoutAndOnItsWave(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	festID := newFest(t, db, "starts-at", "Тройка", systemUserID(t, db))
	numbers := map[string]int{}
	var names []string
	for i := 1; i <= 8; i++ {
		name := fmt.Sprintf("Тройка %d", i)
		names = append(names, name)
		numbers[name] = i
	}
	teams := registerNumberedTeams(t, db, festID, numbers)
	gameID := createSchemeGameFor(t, db, festID, games.Troika, "Тройка", `[defaults]
themes: 1

[scheme]
title: Групповой этап
kind: roundrobin
groups: 2
group_size: 4
metric: total
`, idsFor(t, teams, names))

	type bout struct {
		code         string
		round, wave  int
		block, stage string
	}
	rows, err := db.Query(`
select m.code, m.round, m.wave, s.block_code, s.code from matches m join stages s on s.id = m.stage_id
where m.game_id = ? order by m.position, m.id`, gameID)
	if err != nil {
		t.Fatal(err)
	}
	var bouts []bout
	for rows.Next() {
		var b bout
		if err := rows.Scan(&b.code, &b.round, &b.wave, &b.block, &b.stage); err != nil {
			t.Fatal(err)
		}
		bouts = append(bouts, b)
	}
	rows.Close()
	first := bouts[0]
	if first.round == 0 {
		t.Fatalf("the group bouts carry no round: %+v", bouts)
	}
	sameWave := 0
	for _, b := range bouts {
		if b.block == first.block && b.round == first.round && b.wave == first.wave {
			sameWave++
		}
	}
	if sameWave < 2 {
		t.Fatalf("the first wave has %d bouts, want both groups' (%+v)", sameWave, bouts)
	}

	set := func(code, time string, wave bool) (int, string) {
		resp := scopedAPIRequest(t, srv, http.MethodPost,
			fmt.Sprintf("/api/fest/%d/games/%d/matches/%s/starts-at", festID, gameID, code),
			map[string]any{"time": time, "wave": wave}, token)
		return resp.Code, resp.Body.String()
	}
	startsAt := func(code string) string {
		var at sql.NullString
		if err := db.QueryRow(`select starts_at from matches where game_id = ? and code = ?`, gameID, code).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at.String
	}

	// One bout: typed loosely, kept as HH:MM, and in the bout's own view.
	if code, body := set(first.code, "9.05", false); code != http.StatusOK || !strings.Contains(body, `"startsAt":"09:05"`) {
		t.Fatalf("one bout: %d %s", code, body)
	}
	for _, b := range bouts[1:] {
		if startsAt(b.code) != "" {
			t.Fatalf("%s got a time it was not given", b.code)
		}
	}
	// The fest view carries it for the Сетка, and leaves the others alone.
	resp := scopedAPIRequest(t, srv, http.MethodGet, fmt.Sprintf("/api/fest/%d/games/%d", festID, gameID), nil, token)
	if body := resp.Body.String(); strings.Count(body, `"startsAt":"09:05"`) != 1 {
		t.Fatalf("the fest view: %d bouts with the time, want 1", strings.Count(body, `"startsAt"`))
	}

	// The whole wave: every bout of the block, round and wave, and no other.
	if code, body := set(first.code, "10:30", true); code != http.StatusOK {
		t.Fatalf("the wave: %d %s", code, body)
	}
	for _, b := range bouts {
		inWave := b.block == first.block && b.round == first.round && b.wave == first.wave
		if got := startsAt(b.code); (got == "10:30") != inWave {
			t.Fatalf("%s (round %d, wave %d) has %q after the wave was set", b.code, b.round, b.wave, got)
		}
	}

	// Cleared, and refused when it is no time.
	if code, _ := set(first.code, "", false); code != http.StatusOK || startsAt(first.code) != "" {
		t.Fatalf("clear: %d, %q", code, startsAt(first.code))
	}
	for _, bad := range []string{"25:00", "завтра", "10:7"} {
		if code, body := set(first.code, bad, false); code != http.StatusBadRequest || !strings.Contains(body, "10:30") {
			t.Fatalf("%q: %d %s", bad, code, body)
		}
	}
}
