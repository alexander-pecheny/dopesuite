package tests

import (
	"net/url"
	"testing"
)

// A Тройка created with no troika ticked and no seed declared seats every
// troika of the fest, in the order of applications. It used to fall back to
// what «none ticked» means for a team game — the fest's teams — and a Тройка
// never seats those: its Составы showed «Участник 1…8» with nobody in them.
func TestTroikaWithNoneTickedSeatsTheFestTroikas(t *testing.T) {
	t.Parallel()
	srv, festID, _, createGame := troikaSeedFest(t)
	db := srv.Eng().DB
	dsl := "[scheme]\nkind: roundrobin\ngroup_size: 4\nthemes: 6\nmetric: total\npoints: [1, 0.5, 0]\n"
	gameID := createGame(url.Values{"game_type": {"troika"}, "troika_dsl": {dsl}})

	rows, err := db.Query(`
select p.name, p.assembled from game_participants gp join participants p on p.id = gp.participant_id
where gp.game_id = ? and p.fest_id = ? order by gp.position`, gameID, festID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		var assembled bool
		if err := rows.Scan(&name, &assembled); err != nil {
			t.Fatal(err)
		}
		if !assembled {
			t.Fatalf("the game seats %s, a fest team", name)
		}
		names = append(names, name)
	}
	want := []string{"Ромашка", "Лютик", "Василёк", "По коням"}
	if len(names) != len(want) {
		t.Fatalf("seated %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("seated %v, want %v (the order of applications)", names, want)
		}
	}
}
