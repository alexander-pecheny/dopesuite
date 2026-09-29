package tests

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"dope/dope/domain/imports"

	"github.com/xuri/excelize/v2"
	"pecheny.me/dopecore/session"
)

func postSheet(t *testing.T, game *serverGame, book []byte, preview bool) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "roster.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	part.Write(book)
	form.Close()
	path := fmt.Sprintf("/api/fest/%d/teams/xlsx", game.festID)
	if preview {
		path += "?preview=1"
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: game.token})
	resp := httptest.NewRecorder()
	game.srv.HandleScopedAPI(resp, req)
	return resp
}

// The fest roster goes out as a sheet and comes back in (ADR-0024): the host
// downloads it, moves a person, adds a team with a one-off name and a Flag in
// the sheet, and loads it. The preview says what changes and writes nothing;
// the load sets the teams the sheet names and leaves the rest alone.
func TestRosterSheetRoundTrip(t *testing.T) {
	srv := newAuthTestServer(t)
	srv.SetEditBatchWindow(time.Millisecond)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	festID := newFest(t, db, "sheet", "Фест", systemUserID(t, db))
	game := &serverGame{t: t, srv: srv, festID: festID, token: token}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, siteTeams(map[int64][]string{1: {"Аня", "Борис"}, 2: {"Вера"}, 3: {"Глеб"}}), imports.RosterChoice{}); err != nil {
		t.Fatalf("import: %v", err)
	}

	resp := scopedAPIRequest(t, srv, http.MethodGet, fmt.Sprintf("/api/fest/%d/teams/export.xlsx", festID), nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("export: %d %s", resp.Code, resp.Body.String())
	}
	book, err := excelize.OpenReader(bytes.NewReader(resp.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := book.GetRows(book.GetSheetList()[0])
	if len(rows) != 5 || rows[0][1] != "Команда" || rows[1][1] != "Бобры" || rows[1][5] != "Аня" || rows[1][6] != "101" {
		t.Fatalf("exported rows = %v", rows)
	}

	// The host's sheet: Бобры and Зубры only (Совы are not in it), Борис
	// moved to Зубры, and a new team with a Flag.
	edited := excelize.NewFile()
	sheet := edited.GetSheetName(0)
	for i, row := range [][]any{
		{"Команда", "Город", "ID команды", "Зачёты", "Игрок", "ID игрока"},
		{"Бобры", "Брест", 1, "", "Аня", 101},
		{"Зубры", "Брест", 2, "", "Вера", 201},
		{"Зубры", "Брест", 2, "", "Борис", 102},
		{"Сборная", "Минск", "", "Студ", "Дина Новикова", ""},
	} {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		edited.SetSheetRow(sheet, cell, &row)
	}
	var out bytes.Buffer
	edited.Write(&out)

	before := festRosterNow(t, game)
	resp = postSheet(t, game, out.Bytes(), true)
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `"added_teams":["Сборная (Минск)"]`) ||
		!strings.Contains(resp.Body.String(), `"removed":["Борис"]`) {
		t.Fatalf("preview: %d %s", resp.Code, resp.Body.String())
	}
	if got := festRosterNow(t, game); !reflect.DeepEqual(got, before) {
		t.Fatalf("the preview wrote: %v", got)
	}
	if resp = postSheet(t, game, out.Bytes(), false); resp.Code != http.StatusOK {
		t.Fatalf("load: %d %s", resp.Code, resp.Body.String())
	}
	want := []string{"Бобры: Аня", "Зубры: Борис,Вера", "Сборная: Дина", "Совы: Глеб"}
	if got := festRosterNow(t, game); !reflect.DeepEqual(got, want) {
		t.Fatalf("after the load: %v, want %v", got, want)
	}
	var flags string
	if err := db.QueryRow(`select coalesce(group_concat(f.short), '') from fest_team_flags f join fest_teams t on t.id = f.team_id where t.fest_id = ? and t.name = 'Сборная'`, festID).Scan(&flags); err != nil || flags != "Студ" {
		t.Fatalf("Сборная's Flags = %q (%v)", flags, err)
	}
}

// A team the host adds from the rating site, under a one-off name, is the
// same team when the site later lists it: one row, the site's people, and the
// host's name.
func TestAHandTeamFromTheRatingSiteIsMatchedByAnImport(t *testing.T) {
	srv := newAuthTestServer(t)
	srv.SetEditBatchWindow(time.Millisecond)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	festID := newFest(t, db, "late", "Фест", systemUserID(t, db))
	game := &serverGame{t: t, srv: srv, festID: festID, token: token}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, siteTeams(map[int64][]string{1: {"Аня"}}), imports.RosterChoice{}); err != nil {
		t.Fatalf("import: %v", err)
	}
	resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/teams", festID),
		map[string]any{"rating_id": 3, "name": "Совы-разовые", "city": "Брест", "players": []editPlayer{{301, "Глеб", ""}}}, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("add from the rating site: %d %s", resp.Code, resp.Body.String())
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/teams", festID),
		map[string]any{"rating_id": 3, "name": "Совы", "players": []editPlayer{}}, token); resp.Code != http.StatusBadRequest {
		t.Fatalf("the same rating team twice: %d", resp.Code)
	}
	// The site now registers Совы with Глеб and Жора.
	site := siteTeams(map[int64][]string{1: {"Аня"}, 3: {"Глеб", "Жора"}})
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, site, imports.RosterChoice{}); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if got := festRosterNow(t, game); !reflect.DeepEqual(got, []string{"Бобры: Аня", "Совы-разовые: Глеб,Жора"}) {
		t.Fatalf("after the site listed the team: %v", got)
	}
}
