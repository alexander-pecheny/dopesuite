package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"dope/dope/domain/gamebuild"
	"dope/dope/platform/roles"
	"dope/dope/storage/store"
)

// A host swaps two бои of one круг when a player is late: each is played in
// the other's заход, at its venue, and keeps its name and its seats. The page
// reads the order from the scheme, which follows, and a recompile keeps it.
func TestABoutMovesToAnotherSlotOfItsRound(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	db := srv.Eng().DB
	res, err := db.Exec(`insert into fests(title, revision, created_at, updated_at) values('Кубок', 1, '2026-11-12', '2026-11-12')`)
	if err != nil {
		t.Fatal(err)
	}
	festID, _ := res.LastInsertId()
	seedFestTeams(t, db, festID, 4)
	const dsl = "[defaults]\nquestions: 5\nvenues: 2\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\n"
	gameID := createSchemeGame(t, db, festID, "brain", "Брейн", dsl)
	id, token := createAPITestSession(t, srv, "host")
	addAPITestRole(t, srv, festID, id, roles.Admin)

	type row struct {
		code, slotOf, seats string
		round, wave, pos    int
		venue               sql.NullInt64
	}
	rows := func() map[string]row {
		t.Helper()
		out := map[string]row{}
		list, err := store.CollectRows(t.Context(), db, `
select m.code, m.slot_of, m.round, m.wave, m.position, m.venue_id,
       (select group_concat(participant_id) from match_slots where match_id = m.id)
from matches m where m.game_id = ?`, []any{gameID}, func(rs *sql.Rows) (row, error) {
			var r row
			return r, rs.Scan(&r.code, &r.slotOf, &r.round, &r.wave, &r.pos, &r.venue, &r.seats)
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range list {
			out[r.code] = r
		}
		return out
	}
	schemeOrder := func() []string {
		t.Helper()
		var raw string
		if err := db.QueryRow(`select scheme_json from games where id = ?`, gameID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var scheme store.FestScheme
		if err := json.Unmarshal([]byte(raw), &scheme); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range scheme.Stages[0].Matches {
			out = append(out, fmt.Sprintf("%s r%d w%d v%d", m.Code, m.BlockRound, m.Wave, m.Venue))
		}
		return out
	}

	before := rows()
	var first, second, other string
	for code, r := range before {
		switch {
		case r.round == 1 && r.pos == 1:
			first = code
		case r.round == 1 && r.pos == 2:
			second = code
		case r.round == 2 && other == "":
			other = code
		}
	}
	if first == "" || second == "" || other == "" {
		t.Fatalf("the бои are %+v", before)
	}
	orderBefore := schemeOrder()

	swap := func(code, with string) *strings.Builder {
		t.Helper()
		resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games/%d/matches/%s/slot", festID, gameID, code),
			map[string]any{"with": with}, token)
		var b strings.Builder
		fmt.Fprintf(&b, "%d %s", resp.Code, resp.Body.String())
		return &b
	}
	if got := swap(first, second).String(); !strings.HasPrefix(got, "200") {
		t.Fatalf("the swap: %s", got)
	}
	after := rows()
	a, b := after[first], after[second]
	if a.pos != before[second].pos || a.wave != before[second].wave || a.venue != before[second].venue || a.slotOf != second ||
		b.pos != before[first].pos || b.wave != before[first].wave || b.venue != before[first].venue || b.slotOf != first {
		t.Fatalf("after the swap %s is %+v and %s is %+v; before %+v and %+v", first, a, second, b, before[first], before[second])
	}
	if a.seats != before[first].seats || b.seats != before[second].seats {
		t.Fatalf("the swap moved seats: %+v %+v", a, b)
	}
	orderAfter := schemeOrder()
	if orderAfter[0] == orderBefore[0] || !strings.HasPrefix(orderAfter[0], second+" ") || !strings.HasPrefix(orderAfter[1], first+" ") {
		t.Fatalf("the scheme's order went from %v to %v", orderBefore, orderAfter)
	}

	// A recompile deals the slots again and keeps the move.
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := gamebuild.Recompile(context.Background(), tx, festID, gameID, dsl); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := rows(); got[first] != a || got[second] != b {
		t.Fatalf("the recompile lost the move: %+v %+v", got[first], got[second])
	}
	if got := schemeOrder(); fmt.Sprint(got) != fmt.Sprint(orderAfter) {
		t.Fatalf("the recompile dealt %v, want %v", got, orderAfter)
	}

	// Swapping back undoes the move.
	if got := swap(second, first).String(); !strings.HasPrefix(got, "200") {
		t.Fatalf("the swap back: %s", got)
	}
	if got := rows(); got[first].slotOf != "" || got[second].slotOf != "" || got[first].pos != before[first].pos {
		t.Fatalf("after the swap back: %+v %+v", got[first], got[second])
	}

	// Only бои of one круг share its slots, and a бой that has begun keeps
	// its own.
	if got := swap(first, other).String(); !strings.HasPrefix(got, "400") || !strings.Contains(got, "разных кругов") {
		t.Fatalf("a swap across круги: %s", got)
	}
	if _, err := db.Exec(`update matches set status = 'finished' where game_id = ? and code = ?`, gameID, second); err != nil {
		t.Fatal(err)
	}
	if got := swap(first, second).String(); !strings.HasPrefix(got, "400") || !strings.Contains(got, "уже начат") {
		t.Fatalf("a swap with a finished бой: %s", got)
	}
}
