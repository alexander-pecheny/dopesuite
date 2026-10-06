package tests

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"dope/dope/domain/gamebuild"
)

// A Game that seats the whole fest numbers its teams by their fest numbers.
// With gaps in them (1, 2, 5, 7) a recompile compiled the Game for 1…4 and
// emptied every seat of the teams numbered 5 and 7. It now compiles for the
// numbers the teams sit under.
func TestRecompileKeepsTheNumbersTheGameDealt(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	for i, n := range []int{1, 2, 5, 7} {
		if _, err := db.Exec(`insert into fest_teams(fest_id, name, city, position, number) values(?, ?, '', ?, ?)`,
			festID, []string{"Альфа", "Бета", "Гамма", "Дельта"}[i], i+1, n); err != nil {
			t.Fatal(err)
		}
	}
	const dsl = "[scheme]\nkind: roundrobin\ngroup_size: 4\nquestions: 12\n"
	game := createSchemeGameFor(t, db, festID, "brain", "Брейн", dsl, nil)
	seats := func() []string {
		t.Helper()
		rows, err := db.Query(`
select m.code || '/' || ms.slot_index || '=' || coalesce(p.name, '') from match_slots ms
join matches m on m.id = ms.match_id left join participants p on p.id = ms.participant_id
where m.game_id = ? order by m.code, ms.slot_index`, game)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		return out
	}
	before := seats()
	withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return gamebuild.Recompile(ctx, tx, festID, game, dsl+"\n")
	})
	if after := seats(); !slices.Equal(after, before) {
		t.Fatalf("seats after the recompile = %v, want %v", after, before)
	}
}
