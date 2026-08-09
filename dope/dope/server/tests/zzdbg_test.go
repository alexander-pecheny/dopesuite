package tests

import (
	"database/sql"
	"testing"
)

func TestDbgEntrants(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	all := seedParticipants(t, db, festID, 4)
	if _, err := db.Exec(`insert into fest_teams(fest_id, name, city, position, number) values(?, 'Не играет', '', 99, null)`, festID); err != nil {
		t.Fatal(err)
	}
	gameID := createSchemeGameFor(t, db, festID, "brain", "Брейн",
		"[scheme]\ntype: roundrobin\nteams_in_group: 4\nquestions: 5\n", all)
	rows, _ := db.Query(`select participant_id, position, number from game_participants where game_id = ? order by position`, gameID)
	defer rows.Close()
	for rows.Next() {
		var p, pos, n sql.NullInt64
		_ = rows.Scan(&p, &pos, &n)
		t.Logf("entrant participant=%v position=%v number=%v", p.Int64, pos.Int64, n.Int64)
	}
	var cnt int
	_ = db.QueryRow(`select count(*) from game_assignments where game_id = ?`, gameID).Scan(&cnt)
	t.Logf("assignments=%d", cnt)
}
