package tests

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/xuri/excelize/v2"

	"dope/dope/domain/fixture"
)

// Every Game of the fixture fest, one of each format played to the end,
// downloads as a workbook with its results in it. Личная СИ used to export
// its Game-level document through КСИ's sheets, which came out empty.
func TestEveryFormatExportsItsResults(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	db := srv.Eng().DB
	festID, err := fixture.Build(context.Background(), db, fixture.Options{Slug: "export", Owner: systemUserID(t, db)})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`select id, game_type from games where fest_id = ? order by position`, festID)
	if err != nil {
		t.Fatal(err)
	}
	type game struct {
		id   int64
		kind string
	}
	var found []game
	for rows.Next() {
		var g game
		if err := rows.Scan(&g.id, &g.kind); err != nil {
			t.Fatal(err)
		}
		found = append(found, g)
	}
	rows.Close()
	token := createTestSession(t, srv, systemUserID(t, db))
	for _, g := range found {
		resp := scopedAPIRequest(t, srv, http.MethodGet, fmt.Sprintf("/api/fest/%d/games/%d/export.xlsx", festID, g.id), nil, token)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s: export answered %d", g.kind, resp.Code)
		}
		book, err := excelize.OpenReader(bytes.NewReader(resp.Body.Bytes()))
		if err != nil {
			t.Fatalf("%s: %v", g.kind, err)
		}
		numbers := 0
		for _, sheet := range book.GetSheetList() {
			cells, _ := book.GetRows(sheet)
			for _, row := range cells {
				for _, cell := range row {
					if _, err := fmt.Sscan(cell, new(float64)); err == nil {
						numbers++
					}
				}
			}
		}
		if numbers < 10 {
			t.Errorf("%s: the export holds %d numbers; a played Game's results are missing", g.kind, numbers)
		}
	}
}
