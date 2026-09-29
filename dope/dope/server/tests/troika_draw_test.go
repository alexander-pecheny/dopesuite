package tests

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"dope/dope/domain/games"
	"dope/dope/storage/store"
)

// Троечка §5.3: the play-off's first bouts are drawn on the day. Each pairs a
// group winner with a runner-up of another group. With `draw: true` the
// bracket's opening seats are Draw Slots: empty until the groups are played
// out, then offering every group's first (or second) place, and refusing a
// runner-up drawn against its own group's winner.
func TestTroikaPlayOffIsDrawnApartByGroup(t *testing.T) {
	srv := newAuthTestServer(t)
	srv.SetEditBatchWindow(time.Millisecond)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	festID := newFest(t, db, "troika-draw", "Троечка", systemUserID(t, db))
	var names []string
	numbers := map[string]int{}
	for i := 1; i <= 16; i++ {
		name := fmt.Sprintf("Тройка %d", i)
		names = append(names, name)
		numbers[name] = i
	}
	teams := registerNumberedTeams(t, db, festID, numbers)
	gameID := createSchemeGameFor(t, db, festID, games.Troika, "Троечка", `[defaults]
themes: 1
points: [1, 0.5, 0]

[scheme]
title: Групповой этап
kind: roundrobin
groups: 4
group_size: 4
proceeding_participants: 2
metric: total
sorting: [points, taken]
---
title: Плей-офф
kind: single_elimination
participants: 8
draw: true
metric: total
points: [1, 0, 0]
sorting: [points, taken]
`, idsFor(t, teams, names))
	game := &serverGame{t: t, srv: srv, festID: festID, gameID: gameID, gameType: games.Troika, token: token}
	game.via = httpTransport{game}

	slots := drawSlotsOf(t, game, "s2-r1")
	if len(slots) != 8 {
		t.Fatalf("drawn seats: %d, want 8", len(slots))
	}
	for _, slot := range slots {
		if len(slot.Candidates) != 0 || !slot.Apart {
			t.Fatalf("%s before the groups: %+v", slot.Code, slot)
		}
	}

	// Every group bout: the side seated first takes one question more, so the
	// lower seed of each bout wins and each group ranks cleanly.
	rows, err := db.Query(`
select m.id, m.code from matches m join stages s on s.id = m.stage_id
where m.game_id = ? and s.code like 's1-%' order by m.id`, gameID)
	if err != nil {
		t.Fatal(err)
	}
	type bout struct {
		id   int64
		code string
	}
	var groupBouts []bout
	for rows.Next() {
		var b bout
		if err := rows.Scan(&b.id, &b.code); err != nil {
			t.Fatal(err)
		}
		groupBouts = append(groupBouts, b)
	}
	rows.Close()
	for _, b := range groupBouts {
		ops := []map[string]any{{"path": []any{"sides", 0, "themes", 0, "answers", 0, 0}, "value": "right"}}
		if err := game.via.patch(b.id, b.code, ops); err != nil {
			t.Fatalf("marks %s: %v", b.code, err)
		}
		if err := game.via.finish(b.id, b.code); err != nil {
			t.Fatalf("finish %s: %v", b.code, err)
		}
	}

	slots = drawSlotsOf(t, game, "s2-r1")
	winners, runnersUp := slots[0].Candidates, slots[1].Candidates
	if len(winners) != 4 || len(runnersUp) != 4 {
		t.Fatalf("candidates: %d winners, %d runners-up; want 4 and 4", len(winners), len(runnersUp))
	}
	draw := func(slot string, participant int64) int {
		resp := scopedAPIRequest(t, srv, http.MethodPut,
			fmt.Sprintf("/api/fest/%d/games/%d/draw", festID, gameID),
			map[string]any{"slot": slot, "participant": participant}, token)
		return resp.Code
	}
	// Bout 1: group 1's winner. Its own runner-up is refused, another's taken.
	if code := draw(slots[0].Code, winners[0].ID); code != http.StatusOK {
		t.Fatalf("drawing a winner: %d", code)
	}
	var own, other store.DrawCandidateView
	for _, candidate := range runnersUp {
		if candidate.Source == winners[0].Source {
			own = candidate
		} else if other.ID == 0 {
			other = candidate
		}
	}
	if own.ID == 0 || other.ID == 0 {
		t.Fatalf("runners-up %+v do not name their groups", runnersUp)
	}
	if code := draw(slots[1].Code, own.ID); code != http.StatusBadRequest {
		t.Fatalf("a runner-up against its own group's winner: %d, want 400", code)
	}
	if code := draw(slots[1].Code, other.ID); code != http.StatusOK {
		t.Fatalf("a runner-up of another group: %d", code)
	}
	// The same check holds the other way round: a winner drawn second.
	if code := draw(slots[2].Code, 0); code != http.StatusOK {
		t.Fatalf("clearing an empty seat: %d", code)
	}
	if code := draw(slots[3].Code, own.ID); code != http.StatusOK {
		t.Fatalf("a runner-up into an empty bout: %d", code)
	}
	var ownWinner int64
	for _, candidate := range winners {
		if candidate.Source == own.Source {
			ownWinner = candidate.ID
		}
	}
	if code := draw(slots[2].Code, ownWinner); code != http.StatusBadRequest {
		t.Fatalf("a winner against its own group's runner-up: %d, want 400", code)
	}

	var seated int64
	if err := db.QueryRow(`
select ms.participant_id from match_slots ms join matches m on m.id = ms.match_id
where m.game_id = ? and m.code = 's2-r1-m1' and ms.slot_index = 1`, gameID).Scan(&seated); err != nil {
		t.Fatal(err)
	}
	if seated != other.ID {
		t.Fatalf("bout 1 seats %d as the runner-up, want %d", seated, other.ID)
	}
}
