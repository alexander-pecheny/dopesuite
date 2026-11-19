package tests

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"dope/dope/platform/realtime"
	dopeserver "dope/dope/server"

	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
	"github.com/xuri/excelize/v2"
)

// Polyfest's КИнСбФ seeds on the ОД's first game day, whatever has been played
// since: `seed: od-1[tours 1]` ranks the ОД's table after tour 1 alone, kept
// to the youth/fest teams (division: -Студ).
func TestPolyfestKinsbfSeedsOnTheOdsFirstDay(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedFestTeams(t, db, festID, 20)
	for n := 1; n <= 4; n++ {
		if _, err := db.Exec(`
insert into fest_team_flags(team_id, position, short, full)
select id, 0, 'Студ', 'Студенческая команда' from fest_teams where fest_id = ? and number = ?`, festID, n); err != nil {
			t.Fatal(err)
		}
	}
	odID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "od", "od_tours": "2", "od_questions": "2"})
	var odCode string
	if err := db.QueryRow(`select code from games where id = ?`, odID).Scan(&odCode); err != nil {
		t.Fatal(err)
	}
	// Team 20 takes the one question of tour 1 anybody takes; team 5 takes
	// both of tour 2 and wins the ОД. Team 1 is a student team and leads
	// everything, which the youth seed must not see.
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, odID),
		map[string]any{"ops": []map[string]any{
			{"path": []any{"entries"}, "value": [][]int{{1, 20}, {1}, {1, 5}, {1, 5}}},
			{"path": []any{"completed"}, "value": []bool{true, true, true, true}},
		}}, token); resp.Code != http.StatusOK {
		t.Fatalf("od state: %d %s", resp.Code, resp.Body.String())
	}
	youth := readFile(t, "../../../scripts/polyfest/kinsbf-youth.dsl")
	numberOf := teamNumbers(t, db, festID)
	firstSeed := func(seed string) int {
		t.Helper()
		dsl := strings.Replace(youth, "seed: od-1[tours 1..3]", "seed: "+seed, 1)
		gameID := createSchemeGame(t, db, festID, "brain", "КИнСбФ "+seed, dsl)
		resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games/%d/seed-import/run", festID, gameID), nil, token)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s: seed-import %d %s", seed, resp.Code, resp.Body.String())
		}
		view := decodeJSON[esSeedView](t, resp)
		if len(view.Rows) != 16 {
			t.Fatalf("%s: imported %d teams, want the 16 youth/fest teams", seed, len(view.Rows))
		}
		return numberOf[view.Rows[0].TeamID]
	}
	if got := firstSeed(odCode); got != 5 {
		t.Fatalf("the whole ОД seeds team %d first, want 5", got)
	}
	if got := firstSeed(odCode + "[tours 1]"); got != 20 {
		t.Fatalf("the ОД's tour 1 seeds team %d first, want 20", got)
	}
	// The Участники tab offers the table over those tours beside the whole of
	// it and preselects it, and taking the list from it keeps to them.
	gameID := lastGameID(t, db, festID)
	type picker struct {
		Sources []struct {
			Game  string `json:"game"`
			Tours []int  `json:"tours"`
		} `json:"sources"`
		Preselect struct {
			Game  string `json:"game"`
			Tours []int  `json:"tours"`
		} `json:"preselect"`
		Rows []struct {
			TeamID int64 `json:"teamID"`
		} `json:"rows"`
	}
	entrants := fmt.Sprintf("/api/fest/%d/games/%d/entrants", festID, gameID)
	view := decodeJSON[picker](t, scopedAPIRequest(t, srv, http.MethodGet, entrants, nil, token))
	offered := 0
	for _, source := range view.Sources {
		if source.Game == odCode && fmt.Sprint(source.Tours) == "[1]" {
			offered++
		}
	}
	if offered != 1 || fmt.Sprint(view.Preselect.Tours) != "[1]" {
		t.Fatalf("the picker offers %+v, preselects %+v; want the ОД over tour 1", view.Sources, view.Preselect)
	}
	resp := scopedAPIRequest(t, srv, http.MethodPost, entrants+"/import",
		map[string]any{"kind": "game", "game": odCode, "division": "-Студ", "tours": []int{1}}, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("import from the picker: %d %s", resp.Code, resp.Body.String())
	}
	if got := numberOf[decodeJSON[picker](t, resp).Rows[0].TeamID]; got != 20 {
		t.Fatalf("the picker's ОД over tour 1 seeds team %d first, want 20", got)
	}
}

func lastGameID(t *testing.T, db *sql.DB, festID int64) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`select max(id) from games where fest_id = ?`, festID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// A seed that counts some tours names an ОД's: another Game has none, and
// the brackets take nothing but tours.
func TestASeedCountsToursOfAnOdAlone(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedFestTeams(t, db, festID, 4)
	ksiID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "ksi", "ksi_themes": "2"})
	var ksiCode string
	if err := db.QueryRow(`select code from games where id = ?`, ksiID).Scan(&ksiCode); err != nil {
		t.Fatal(err)
	}
	dsl := func(seed string) string {
		return "[init]\nseed: " + seed + "\n\n[scheme]\nkind: roundrobin\ngroups: 1\ngroup_size: 4\nquestions: 5\npoints: [3, 1, 0]\n"
	}
	for seed, want := range map[string]string{
		ksiCode + "[tours 1]":   "туры есть только у ОД",
		ksiCode + "[1]":         "после кода игры в скобках",
		"random[tours 1]":       "после кода игры в скобках",
		ksiCode + "[tours два]": "после кода игры в скобках",
	} {
		resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games", festID),
			map[string]any{"game_type": "brain", "dsl": dsl(seed)}, token)
		if resp.Code == http.StatusOK || !strings.Contains(resp.Body.String(), want) {
			t.Errorf("seed: %s → %d %s, want a refusal saying %q", seed, resp.Code, resp.Body.String(), want)
		}
	}
}

// Polyfest's personal СИ seeds on its online qualifier, uploaded as a sheet of
// players' names. The sheet used to be matched against the teams alone, so no
// player was ever found; now each name is a fest player, who becomes the
// Game's Participant, and only the players the sheet names do.
func TestPolyfestSiSeedsOnTheQualifierSheet(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	for i := 1; i <= 40; i++ {
		if _, err := db.Exec(`insert into fest_players(fest_id, first_name, last_name) values(?, ?, ?)`, festID, "Игрок", fmt.Sprintf("Номер%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	gameID := createSchemeGame(t, db, festID, "si", "СИ молодёжь", readFile(t, "../../../scripts/polyfest/si-youth.dsl"))
	upload := func(names []string) *httptest.ResponseRecorder {
		t.Helper()
		book := excelize.NewFile()
		book.SetCellValue("Sheet1", "A1", "Игрок")
		for i, name := range names {
			if err := book.SetCellValue("Sheet1", fmt.Sprintf("A%d", i+2), name); err != nil {
				t.Fatal(err)
			}
		}
		var sheet, body bytes.Buffer
		if err := book.Write(&sheet); err != nil {
			t.Fatal(err)
		}
		form := multipart.NewWriter(&body)
		part, err := form.CreateFormFile("file", "qualifier.xlsx")
		if err != nil {
			t.Fatal(err)
		}
		part.Write(sheet.Bytes())
		form.Close()
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/fest/%d/games/%d/seed-import/xlsx", festID, gameID), &body)
		req.Header.Set("Content-Type", form.FormDataContentType())
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		resp := httptest.NewRecorder()
		srv.HandleScopedAPI(resp, req)
		return resp
	}
	if resp := upload([]string{"Игрок Номер1", "Кто-то Другой"}); resp.Code == http.StatusOK || !strings.Contains(resp.Body.String(), `игрок "Кто-то Другой" не найден`) {
		t.Fatalf("an unknown name: %d %s, want it refused as a player", resp.Code, resp.Body.String())
	}
	var names []string
	for i := 40; i > 8; i-- {
		names = append(names, fmt.Sprintf("Игрок Номер%d", i))
	}
	resp := upload(names)
	if resp.Code != http.StatusOK {
		t.Fatalf("seed-import/xlsx: %d %s", resp.Code, resp.Body.String())
	}
	var view struct {
		Rows []struct {
			Name string `json:"name"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Rows) != 32 || view.Rows[0].Name != "Игрок Номер40" || view.Rows[31].Name != "Игрок Номер9" {
		t.Fatalf("seeded %d players, first %+v; want the sheet's 32 in its order", len(view.Rows), view.Rows)
	}
	var participants int
	if err := db.QueryRow(`select count(*) from participants where fest_id = ? and roster = 'player'`, festID).Scan(&participants); err != nil {
		t.Fatal(err)
	}
	if participants != 32 {
		t.Fatalf("%d player Participants, want the 32 the sheet names", participants)
	}
}

// kinsbfOnAnOd is a КИнСбФ (the youth scheme, every team in it) seeded on
// the first two tours of an ОД of two tours of two questions, whose entries
// are given, and the list it took: the fest's numbers in seed order.
func kinsbfOnAnOd(t *testing.T, teams int, entries [][]int) (srv *dopeserver.Server, token string, base string, festID int64, listed []int) {
	t.Helper()
	srv = newAuthTestServer(t)
	festID, _ = scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token = createTestSession(t, srv, systemUserID(t, db))
	seedFestTeams(t, db, festID, teams)
	odID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "od", "od_tours": "2", "od_questions": "2"})
	var odCode string
	if err := db.QueryRow(`select code from games where id = ?`, odID).Scan(&odCode); err != nil {
		t.Fatal(err)
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, odID),
		map[string]any{"ops": []map[string]any{
			{"path": []any{"entries"}, "value": entries},
			{"path": []any{"completed"}, "value": []bool{true, true, true, true}},
		}}, token); resp.Code != http.StatusOK {
		t.Fatalf("od state: %d %s", resp.Code, resp.Body.String())
	}
	dsl := readFile(t, "../../../scripts/polyfest/kinsbf-youth.dsl")
	dsl = strings.Replace(dsl, "seed: od-1[tours 1..3]", "seed: "+odCode+"[tours 1..2]", 1)
	dsl = strings.Replace(dsl, "division: -Студ\n", "", 1)
	gameID := createSchemeGame(t, db, festID, "brain", "КИнСбФ", dsl)
	base = fmt.Sprintf("/api/fest/%d/games/%d", festID, gameID)
	resp := scopedAPIRequest(t, srv, http.MethodPost, base+"/entrants/import",
		map[string]any{"kind": "game", "game": odCode, "tours": []int{1, 2}}, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("import: %d %s", resp.Code, resp.Body.String())
	}
	numberOf := teamNumbers(t, db, festID)
	for _, row := range decodeJSON[esSeedView](t, resp).Rows {
		listed = append(listed, numberOf[row.TeamID])
	}
	return srv, token, base, festID, listed
}

// Teams level on the ОД's questions go by рейтинг, not by the ОД's own order
// of teams, which is alphabetical: at Polyfest the hosts re-sorted КИнСбФ's
// list by hand. Teams 2 and 3 take one question each; 3's is the one nobody
// else took, so 3 comes first.
func TestAnOdSeedBreaksTiesByRating(t *testing.T) {
	t.Parallel()
	_, _, _, _, listed := kinsbfOnAnOd(t, 20, [][]int{{1, 2, 4, 5}, {3}, {1}, {1}})
	if fmt.Sprint(listed[:5]) != "[1 3 2 4 5]" {
		t.Fatalf("seeded %v, want 1, then 3 over 2, 4 and 5 on рейтинг", listed)
	}
}

// A decline deals the seats again, and every page drawing those bouts must
// be told: a page draws a bout's teams from the bout's own view, so with only
// the fest view sent, every page but the host's own kept the old seating.
func TestADeclineSendsTheBoutsItReseated(t *testing.T) {
	t.Parallel()
	srv, token, base, festID, listed := kinsbfOnAnOd(t, 24, [][]int{{1, 2, 3}, {1, 2}, {1}, {}})
	if listed[0] != 1 {
		t.Fatalf("seeded %v, want team 1 first", listed)
	}
	db := srv.Eng().DB
	var first int64
	if err := db.QueryRow(`select id from participants where fest_id = ? and number = 1`, festID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	ch := make(chan realtime.Event, 256)
	srv.Eng().RT.AddSubscriber(festID, ch, false, 0)
	defer srv.Eng().RT.RemoveSubscriber(festID, ch)

	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("%s/entrants/%d", base, first),
		map[string]any{"declined": true}, token); resp.Code != http.StatusOK {
		t.Fatalf("decline: %d %s", resp.Code, resp.Body.String())
	}
	// Seed 1 sits in a bout of every round of its group; team 2 takes it in each.
	sent := map[string]string{}
	for len(ch) > 0 {
		var env struct {
			Scope string          `json:"scope"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal((<-ch).Data, &env); err == nil && strings.HasPrefix(env.Scope, "match:") {
			sent[env.Scope] = string(env.Data)
		}
	}
	reseated := 0
	for scope, data := range sent {
		if strings.Contains(data, `"Участник 1"`) {
			t.Fatalf("%s still seats the declined team: %s", scope, data)
		}
		if strings.Contains(data, `"Участник 2"`) {
			reseated++
		}
	}
	if reseated < 2 {
		t.Fatalf("sent %d bouts with team 2 in seed 1's seats, want seed 1's group bouts; sent %v", reseated, slices.Sorted(maps.Keys(sent)))
	}
	// The grid draws each seat's seed number from the fest view: team 2 now
	// holds seed 1.
	view := decodeJSON[seededFestView](t, scopedAPIRequest(t, srv, http.MethodGet, base, nil, token))
	seeds := map[string]int{}
	for _, stage := range view.Stages {
		for _, match := range stage.Matches {
			for _, seat := range match.Participants {
				if seat.Seed > 0 {
					seeds[seat.Name] = seat.Seed
				}
			}
		}
	}
	if seeds["Участник 2"] != 1 || seeds["Участник 1"] != 0 {
		t.Fatalf("the fest view's seeds = %v, want Участник 2 on seed 1 and Участник 1 on none", seeds)
	}
}

// seededFestView is the fest view as far as the grid's seed numbers go.
type seededFestView struct {
	Stages []struct {
		Matches []struct {
			Participants []struct {
				Name string `json:"name"`
				Seed int    `json:"seed"`
			} `json:"participants"`
		} `json:"matches"`
	} `json:"stages"`
}
