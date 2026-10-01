package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
	"pecheny.me/dopecore/session"

	"dope/dope/domain/core"
	"dope/dope/domain/entrants"
	dopeserver "dope/dope/server"
)

// troikaSeedFest is a fest of four rating teams numbered 1..4 and four
// troikas, one of which shares its name with a rating team — Octobearfest's
// «По коням» was both.
func troikaSeedFest(t *testing.T) (*dopeserver.Server, int64, string, func(url.Values) int64) {
	t.Helper()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	token := createTestSession(t, srv, systemUserID(t, srv.Eng().DB))
	for i, name := range []string{"Астра", "Берёза", "Вяз", "По коням"} {
		if _, err := srv.Eng().DB.Exec(`
insert into fest_teams(fest_id, name, city, position, number) values(?, ?, 'Город', ?, ?)`,
			festID, name, i+1, i+1); err != nil {
			t.Fatalf("insert fest team: %v", err)
		}
	}
	post := func(path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		resp := httptest.NewRecorder()
		srv.HostPageServer().HandleHostRouter(resp, req)
		return resp
	}
	lines := "Ромашка: Иван Иванов, Пётр Петров\nЛютик: Сидор Сидоров, Фома Фомин\n" +
		"Василёк: Лука Лукин, Марк Марков\nПо коням: Олег Олегов, Глеб Глебов\n"
	if resp := post(fmt.Sprintf("/host/fest/%d/troikas", festID), url.Values{"mode": {"lines"}, "lines": {lines}}); resp.Code >= 400 ||
		strings.Contains(resp.Body.String(), "уже занято") {
		t.Fatalf("add troikas: %d %s", resp.Code, resp.Body.String())
	}
	createGame := func(form url.Values) int64 {
		t.Helper()
		if resp := post(fmt.Sprintf("/host/fest/%d/game/new", festID), form); resp.Code != http.StatusSeeOther {
			t.Fatalf("create game status = %d, body %s", resp.Code, resp.Body.String())
		}
		var id int64
		if err := srv.Eng().DB.QueryRow(`select max(id) from games where fest_id = ?`, festID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	return srv, festID, token, createGame
}

func troikaSeatName(t *testing.T, srv *dopeserver.Server, gameID int64, matchCode string, slot int) string {
	t.Helper()
	var name string
	if err := srv.Eng().DB.QueryRow(`
select p.name from match_slots ms
join matches m on m.id = ms.match_id
join participants p on p.id = ms.participant_id
where m.game_id = ? and m.code = ? and ms.slot_index = ?`, gameID, matchCode, slot).Scan(&name); err != nil {
		t.Fatalf("seat %s/%d: %v", matchCode, slot, err)
	}
	return name
}

func assertFestTeamsUntouched(t *testing.T, srv *dopeserver.Server, festID int64) {
	t.Helper()
	rows, err := srv.Eng().DB.Query(`select name from participants where fest_id = ? and assembled = 0 and number is not null order by number`, festID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if got := strings.Join(names, ","); got != "" && got != "Астра,Берёза,Вяз,По коням" {
		t.Fatalf("fest teams renamed by the troika seed: %s", got)
	}
}

// A Троечка's xlsx посев names troikas: the sheet used to be matched against
// the rating teams only, so no troika was ever found. The troika «По коням»
// shares its name with a rating team and must still seat the troika.
func TestTroikaSeedFromXLSXNamesTroikas(t *testing.T) {
	srv, festID, token, createGame := troikaSeedFest(t)
	dsl := "[init]\nseed: xlsx\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\nthemes: 6\nmetric: total\npoints: [1, 0.5, 0]\n"
	gameID := createGame(url.Values{"game_type": {"troika"}, "troika_dsl": {dsl}})

	book := excelize.NewFile()
	for i, name := range []string{"Василёк", "По коням", "Ромашка", "Лютик"} {
		if err := book.SetCellValue("Sheet1", fmt.Sprintf("A%d", i+1), name); err != nil {
			t.Fatal(err)
		}
	}
	var sheet bytes.Buffer
	if err := book.Write(&sheet); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "seed.xlsx")
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
	if resp.Code != http.StatusOK {
		t.Fatalf("seed-import/xlsx = %d, body %s", resp.Code, resp.Body.String())
	}

	// Round one of a group of four pairs 1–2 and 3–4.
	if a, b := troikaSeatName(t, srv, gameID, "s1-g1-1", 0), troikaSeatName(t, srv, gameID, "s1-g1-1", 1); a != "Василёк" || b != "По коням" {
		t.Fatalf("бой 1 = %s vs %s, want Василёк vs По коням", a, b)
	}
	var assembled int
	if err := srv.Eng().DB.QueryRow(`
select count(*) from match_slots ms join matches m on m.id = ms.match_id
join participants p on p.id = ms.participant_id
where m.game_id = ? and p.assembled = 1`, gameID).Scan(&assembled); err != nil {
		t.Fatal(err)
	}
	var seated int
	if err := srv.Eng().DB.QueryRow(`
select count(*) from match_slots ms join matches m on m.id = ms.match_id
where m.game_id = ? and ms.participant_id is not null`, gameID).Scan(&seated); err != nil {
		t.Fatal(err)
	}
	if seated == 0 || assembled != seated {
		t.Fatalf("seated %d, of them troikas %d: every seat must be a troika", seated, assembled)
	}
	assertFestTeamsUntouched(t, srv, festID)
}

// seed: random for a Троечка draws its lot over the troikas, not over the
// rating teams it used to.
func TestTroikaRandomSeedDrawsTroikas(t *testing.T) {
	srv, festID, token, createGame := troikaSeedFest(t)
	dsl := "[init]\nseed: random\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\nthemes: 6\nmetric: total\npoints: [1, 0.5, 0]\n"
	gameID := createGame(url.Values{"game_type": {"troika"}, "troika_dsl": {dsl}})
	if resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games/%d/seed-import/run", festID, gameID), nil, token); resp.Code != http.StatusOK {
		t.Fatalf("seed-import/run = %d, body %s", resp.Code, resp.Body.String())
	}
	var notTroika int
	if err := srv.Eng().DB.QueryRow(`
select count(*) from match_slots ms join matches m on m.id = ms.match_id
join participants p on p.id = ms.participant_id
where m.game_id = ? and p.assembled = 0`, gameID).Scan(&notTroika); err != nil {
		t.Fatal(err)
	}
	if notTroika != 0 {
		t.Fatalf("%d rating-team seats in a Троечка", notTroika)
	}
	assertFestTeamsUntouched(t, srv, festID)
}

// seed: players reads each troika's players' places in another Game. Its
// candidates carried the troika's number inside this Game, and the import read
// that as a fest number: troika №1 found rating team №1 and renamed it.
func TestTroikaPlayersSeedLeavesFestTeamsAlone(t *testing.T) {
	srv, festID, token, createGame := troikaSeedFest(t)
	db := srv.Eng().DB
	// Rosters: two players per rating team, the same people the troikas name.
	people := map[string][2]string{
		"Астра": {"Иван Иванов", "Пётр Петров"}, "Берёза": {"Сидор Сидоров", "Фома Фомин"},
		"Вяз": {"Лука Лукин", "Марк Марков"}, "По коням": {"Олег Олегов", "Глеб Глебов"},
	}
	for team, pair := range people {
		var teamID int64
		if err := db.QueryRow(`select id from fest_teams where fest_id = ? and name = ?`, festID, team).Scan(&teamID); err != nil {
			t.Fatal(err)
		}
		for i, full := range pair {
			first, last, _ := strings.Cut(full, " ")
			res, err := db.Exec(`insert into fest_players(fest_id, first_name, last_name) values(?, ?, ?)`, festID, first, last)
			if err != nil {
				t.Fatal(err)
			}
			playerID, _ := res.LastInsertId()
			if _, err := db.Exec(`insert into fest_team_players(team_id, player_id, roster_order) values(?, ?, ?)`, teamID, playerID, i); err != nil {
				t.Fatal(err)
			}
		}
	}
	odID := createGame(url.Values{"game_type": {"od"}, "od_tours": {"1"}, "od_questions": {"3"}})
	var odCode string
	if err := db.QueryRow(`select code from games where id = ?`, odID).Scan(&odCode); err != nil {
		t.Fatal(err)
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, odID),
		map[string]any{"ops": []map[string]any{
			{"path": []any{"entries"}, "value": [][]int{{4, 2, 1}, {4, 2}, {4}}},
			{"path": []any{"completed"}, "value": []bool{true, true, true}},
		}}, token); resp.Code != http.StatusOK {
		t.Fatalf("od state: %d %s", resp.Code, resp.Body.String())
	}
	var troikas []string
	rows, err := db.Query(`select id from participants where fest_id = ? and assembled = 1 order by id`, festID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		troikas = append(troikas, fmt.Sprint(id))
	}
	rows.Close()
	dsl := fmt.Sprintf("[init]\nseed: players\ngames: [%s]\nplayer.p: place1\nseed.mean: mean(p)\nsorting: [mean asc]\n\n"+
		"[scheme]\nkind: roundrobin\ngroup_size: 4\nthemes: 6\nmetric: total\npoints: [1, 0.5, 0]\n", odCode)
	gameID := createGame(url.Values{"game_type": {"troika"}, "troika_dsl": {dsl}, "entrant_id": troikas})
	resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games/%d/seed-import/run", festID, gameID), nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("seed-import/run = %d, body %s", resp.Code, resp.Body.String())
	}
	assertFestTeamsUntouched(t, srv, festID)
	var notTroika int
	if err := db.QueryRow(`
select count(*) from match_slots ms join matches m on m.id = ms.match_id
join participants p on p.id = ms.participant_id
where m.game_id = ? and p.assembled = 0`, gameID).Scan(&notTroika); err != nil {
		t.Fatal(err)
	}
	if notTroika != 0 {
		t.Fatalf("%d rating-team seats in a Троечка", notTroika)
	}
	// ОД: Гинкго-style entries rank Вяз's… no — team 4 (По коням) took all
	// three, team 2 two, team 1 one: the troika of По коням's players seeds 1st.
	if got := troikaSeatName(t, srv, gameID, "s1-g1-1", 0); got != "По коням" {
		t.Errorf("seed 1 = %s, want the troika По коням (its players' team won the ОД)", got)
	}
}

// A troika may share its name with a rating team (регламент Троечки allows
// it; Octobearfest had «По коням» twice). The team Participant already exists
// here — a team Game made it — and adding the troika used to be refused. A
// team Game's seed import that names «По коням» must still seat the team.
func TestTroikaMayShareARatingTeamsName(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	token := createTestSession(t, srv, systemUserID(t, srv.Eng().DB))
	db := srv.Eng().DB
	for i, name := range []string{"Астра", "Берёза", "Вяз", "По коням"} {
		if _, err := db.Exec(`insert into fest_teams(fest_id, name, city, position, number) values(?, ?, 'Город', ?, ?)`,
			festID, name, i+1, i+1); err != nil {
			t.Fatal(err)
		}
	}
	post := func(path string, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		resp := httptest.NewRecorder()
		srv.HostPageServer().HandleHostRouter(resp, req)
		return resp
	}
	dsl := "[defaults]\nquestions: 5\n\n[init]\nseed: random\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\n"
	if resp := post(fmt.Sprintf("/host/fest/%d/game/new", festID), url.Values{"game_type": {"brain"}, "brain_dsl": {dsl}}); resp.Code != http.StatusSeeOther {
		t.Fatalf("brain: %d %s", resp.Code, resp.Body.String())
	}
	var brainID int64
	db.QueryRow(`select id from games where fest_id = ? and game_type = 'brain'`, festID).Scan(&brainID)
	if resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games/%d/seed-import/run", festID, brainID), nil, token); resp.Code != http.StatusOK {
		t.Fatalf("brain seed: %d %s", resp.Code, resp.Body.String())
	}

	resp := post(fmt.Sprintf("/host/fest/%d/troikas", festID), url.Values{"mode": {"lines"}, "lines": {"По коням: Олег Олегов, Глеб Глебов\n"}})
	if strings.Contains(resp.Body.String(), "уже") {
		t.Fatalf("a troika named like a rating team was refused")
	}
	// Two troikas of one name are still refused.
	resp = post(fmt.Sprintf("/host/fest/%d/troikas", festID), url.Values{"mode": {"lines"}, "lines": {"По коням: Ян Янов, Ум Умов\n"}})
	if !strings.Contains(resp.Body.String(), "уже есть") {
		t.Fatalf("a second troika «По коням» was accepted")
	}

	// Re-import: every seat of the brain is still a rating team.
	if resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games/%d/seed-import/run", festID, brainID), nil, token); resp.Code != http.StatusOK {
		t.Fatalf("brain reseed: %d %s", resp.Code, resp.Body.String())
	}
	var troikaSeats int
	db.QueryRow(`
select count(*) from match_slots ms join matches m on m.id = ms.match_id
join participants p on p.id = ms.participant_id
where m.game_id = ? and p.assembled = 1`, brainID).Scan(&troikaSeats)
	if troikaSeats != 0 {
		t.Fatalf("the troika took %d of the brain's seats", troikaSeats)
	}
}

// The regulations add up places, and teams that share a place share it. In an
// ОД where Астра and Берёза tie for first, a troika of Астра's players and a
// troika of Берёза's both stand at 1.5, and the earlier application seeds
// first. The seed used to read the table's line numbers instead, so Астра —
// listed first on the tie — was 1 and Берёза 2 whatever the order of
// applications.
func TestTroikaPlayersSeedSharesTiedPlaces(t *testing.T) {
	srv, festID, token, createGame := troikaSeedFest(t)
	db := srv.Eng().DB
	people := map[string][2]string{
		"Астра": {"Иван Иванов", "Пётр Петров"}, "Берёза": {"Сидор Сидоров", "Фома Фомин"},
		"Вяз": {"Лука Лукин", "Марк Марков"}, "По коням": {"Олег Олегов", "Глеб Глебов"},
	}
	for team, pair := range people {
		var teamID int64
		if err := db.QueryRow(`select id from fest_teams where fest_id = ? and name = ?`, festID, team).Scan(&teamID); err != nil {
			t.Fatal(err)
		}
		for i, full := range pair {
			first, last, _ := strings.Cut(full, " ")
			res, err := db.Exec(`insert into fest_players(fest_id, first_name, last_name) values(?, ?, ?)`, festID, first, last)
			if err != nil {
				t.Fatal(err)
			}
			playerID, _ := res.LastInsertId()
			if _, err := db.Exec(`insert into fest_team_players(team_id, player_id, roster_order) values(?, ?, ?)`, teamID, playerID, i); err != nil {
				t.Fatal(err)
			}
		}
	}
	odID := createGame(url.Values{"game_type": {"od"}, "od_tours": {"1"}, "od_questions": {"3"}})
	var odCode string
	if err := db.QueryRow(`select code from games where id = ?`, odID).Scan(&odCode); err != nil {
		t.Fatal(err)
	}
	// Астра (1) and Берёза (2) take two each, Вяз (3) one, По коням (4) none.
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, odID),
		map[string]any{"ops": []map[string]any{
			{"path": []any{"entries"}, "value": [][]int{{1, 2, 3}, {1, 2}, {}}},
			{"path": []any{"completed"}, "value": []bool{true, true, true}},
		}}, token); resp.Code != http.StatusOK {
		t.Fatalf("od state: %d %s", resp.Code, resp.Body.String())
	}
	troika := func(name string) string {
		var id int64
		if err := db.QueryRow(`select id from participants where fest_id = ? and assembled = 1 and name = ?`, festID, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(id)
	}
	// Лютик (Берёза's players) applied before Ромашка (Астра's).
	if resp := scopedAPIRequest(t, srv, http.MethodPut, fmt.Sprintf("/api/fest/%d/troikas/%s", festID, troika("Лютик")),
		map[string]any{"name": "Лютик", "players": []string{"Сидор Сидоров", "Фома Фомин"}, "applied": 1}, token); resp.Code != http.StatusOK {
		t.Fatalf("move Лютик's application: %d %s", resp.Code, resp.Body.String())
	}
	entrants := []string{troika("Лютик"), troika("Ромашка"), troika("Василёк"), troika("По коням")}
	dsl := fmt.Sprintf("[init]\nseed: players\ngames: [%s]\nplayer.p: place1\nseed.mean: mean(p)\nsorting: [mean asc]\n\n"+
		"[scheme]\nkind: roundrobin\ngroup_size: 4\nthemes: 6\nmetric: total\npoints: [1, 0.5, 0]\n", odCode)
	gameID := createGame(url.Values{"game_type": {"troika"}, "troika_dsl": {dsl}, "entrant_id": entrants})
	if resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games/%d/seed-import/run", festID, gameID), nil, token); resp.Code != http.StatusOK {
		t.Fatalf("seed-import/run = %d, body %s", resp.Code, resp.Body.String())
	}
	if a, b := troikaSeatName(t, srv, gameID, "s1-g1-1", 0), troikaSeatName(t, srv, gameID, "s1-g1-1", 1); a != "Лютик" || b != "Ромашка" {
		t.Fatalf("seeds 1 and 2 = %s, %s; want Лютик, Ромашка (tied at 1.5, Лютик applied first)", a, b)
	}
}

// A troika whose people are nobody on the fest roster (stand-ins, «болваны»)
// has no places to add up. The seed used to stop there («у команды … нет
// состава»); it now seeds such a troika last and names it.
func TestTroikaPlayersSeedPutsUnknownPeopleLast(t *testing.T) {
	srv, festID, token, createGame := troikaSeedFest(t)
	db := srv.Eng().DB
	people := map[string][2]string{
		"Астра": {"Иван Иванов", "Пётр Петров"}, "Берёза": {"Сидор Сидоров", "Фома Фомин"},
		"Вяз": {"Лука Лукин", "Марк Марков"}, "По коням": {"Олег Олегов", "Глеб Глебов"},
	}
	for team, pair := range people {
		var teamID int64
		if err := db.QueryRow(`select id from fest_teams where fest_id = ? and name = ?`, festID, team).Scan(&teamID); err != nil {
			t.Fatal(err)
		}
		for i, full := range pair {
			first, last, _ := strings.Cut(full, " ")
			res, err := db.Exec(`insert into fest_players(fest_id, first_name, last_name) values(?, ?, ?)`, festID, first, last)
			if err != nil {
				t.Fatal(err)
			}
			playerID, _ := res.LastInsertId()
			if _, err := db.Exec(`insert into fest_team_players(team_id, player_id, roster_order) values(?, ?, ?)`, teamID, playerID, i); err != nil {
				t.Fatal(err)
			}
		}
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/troikas", festID),
		map[string]any{"lines": "Болваны: Болван Первый, Болван Второй\n"}, token); resp.Code != http.StatusOK {
		t.Fatalf("add a stand-in troika: %d %s", resp.Code, resp.Body.String())
	}
	odID := createGame(url.Values{"game_type": {"od"}, "od_tours": {"1"}, "od_questions": {"3"}})
	var odCode string
	if err := db.QueryRow(`select code from games where id = ?`, odID).Scan(&odCode); err != nil {
		t.Fatal(err)
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, odID),
		map[string]any{"ops": []map[string]any{
			{"path": []any{"entries"}, "value": [][]int{{4, 2, 1}, {4, 2}, {4}}},
			{"path": []any{"completed"}, "value": []bool{true, true, true}},
		}}, token); resp.Code != http.StatusOK {
		t.Fatalf("od state: %d %s", resp.Code, resp.Body.String())
	}
	dsl := fmt.Sprintf("[init]\nseed: players\ngames: [%s]\nplayer.p: place1\nseed.mean: mean(p)\nsorting: [mean asc]\n\n"+
		"[scheme]\nkind: roundrobin\ngroup_size: 5\nthemes: 6\nmetric: total\npoints: [1, 0.5, 0]\n", odCode)
	gameID := createGame(url.Values{"game_type": {"troika"}, "troika_dsl": {dsl}})
	scope := core.FestScope{FestID: festID, GameID: gameID}
	if _, err := entrants.Import(srv.Eng(), t.Context(), scope, entrants.Source{Kind: entrants.SourceTroikas}, nil); err != nil {
		t.Fatal(err)
	}
	result, err := entrants.Import(srv.Eng(), t.Context(), scope, entrants.Source{Kind: entrants.SourcePlayers}, nil)
	if err != nil {
		t.Fatalf("the seed stopped at the stand-ins: %v", err)
	}
	rows := result.View.Rows
	if len(rows) != 5 || rows[0].Name != "По коням" || rows[4].Name != "Болваны" {
		var names []string
		for _, row := range rows {
			names = append(names, row.Name)
		}
		t.Fatalf("seeded %v, want По коням first and Болваны last", names)
	}
	if !slices.Equal(result.View.Unranked, []string{"Болваны"}) {
		t.Fatalf("unranked = %v, want [Болваны]", result.View.Unranked)
	}
	// Read back, as the fest broadcast does the moment the import lands.
	read, err := entrants.Load(t.Context(), srv.Eng().DB, scope)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(read.Unranked, []string{"Болваны"}) {
		t.Fatalf("read back: unranked = %v, want [Болваны]", read.Unranked)
	}
}

// The Троечка regulations' last seeding tie-break is the earlier
// application. A troika keeps its place in the order of applications: pasted
// lines take the next places in their order, the host can move one, a list
// taken from the troikas follows that order, and two troikas equal on every
// metric seed by it.
func TestTroikaApplicationOrderBreaksTheLastTie(t *testing.T) {
	srv, festID, token, createGame := troikaSeedFest(t)
	db := srv.Eng().DB
	people := map[string][]string{
		"Астра": {"Иван Иванов", "Пётр Петров", "Анна Аннова", "Бэла Бэлова"}, "Берёза": {"Сидор Сидоров", "Фома Фомин"},
		"Вяз": {"Лука Лукин", "Марк Марков"}, "По коням": {"Олег Олегов", "Глеб Глебов"},
	}
	for team, list := range people {
		var teamID int64
		if err := db.QueryRow(`select id from fest_teams where fest_id = ? and name = ?`, festID, team).Scan(&teamID); err != nil {
			t.Fatal(err)
		}
		for i, full := range list {
			first, last, _ := strings.Cut(full, " ")
			res, err := db.Exec(`insert into fest_players(fest_id, first_name, last_name) values(?, ?, ?)`, festID, first, last)
			if err != nil {
				t.Fatal(err)
			}
			playerID, _ := res.LastInsertId()
			if _, err := db.Exec(`insert into fest_team_players(team_id, player_id, roster_order) values(?, ?, ?)`, teamID, playerID, i); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Поздняя is all Астра, as Ромашка is, and applied fifth.
	if resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/troikas", festID),
		map[string]any{"lines": "Поздняя: Анна Аннова, Бэла Бэлова\n"}, token); resp.Code != http.StatusOK {
		t.Fatalf("add a troika: %d %s", resp.Code, resp.Body.String())
	}
	type troika struct {
		ID      int64  `json:"id"`
		Applied int    `json:"applied"`
		Name    string `json:"name"`
	}
	troikas := func() []troika {
		t.Helper()
		resp := scopedAPIRequest(t, srv, http.MethodGet, fmt.Sprintf("/api/fest/%d/troikas", festID), nil, token)
		var out []troika
		if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	order := func(list []troika) string {
		var parts []string
		for _, tr := range list {
			parts = append(parts, fmt.Sprintf("%d %s", tr.Applied, tr.Name))
		}
		return strings.Join(parts, "; ")
	}
	if got := order(troikas()); got != "1 Ромашка; 2 Лютик; 3 Василёк; 4 По коням; 5 Поздняя" {
		t.Fatalf("troikas: %s, want the lines' order", got)
	}

	odID := createGame(url.Values{"game_type": {"od"}, "od_tours": {"1"}, "od_questions": {"3"}})
	var odCode string
	if err := db.QueryRow(`select code from games where id = ?`, odID).Scan(&odCode); err != nil {
		t.Fatal(err)
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, odID),
		map[string]any{"ops": []map[string]any{
			{"path": []any{"entries"}, "value": [][]int{{1, 2, 3}, {1, 2}, {1}}},
			{"path": []any{"completed"}, "value": []bool{true, true, true}},
		}}, token); resp.Code != http.StatusOK {
		t.Fatalf("od state: %d %s", resp.Code, resp.Body.String())
	}
	dsl := fmt.Sprintf("[init]\nseed: players\ngames: [%s]\nplayer.p: place1\nseed.mean: mean(p)\nsorting: [mean asc]\n\n"+
		"[scheme]\nkind: roundrobin\ngroup_size: 5\nthemes: 6\nmetric: total\npoints: [1, 0.5, 0]\n", odCode)
	gameID := createGame(url.Values{"game_type": {"troika"}, "troika_dsl": {dsl}})
	scope := core.FestScope{FestID: festID, GameID: gameID}
	names := func(view entrants.View) string {
		var out []string
		for _, row := range view.Rows {
			out = append(out, row.Name)
		}
		return strings.Join(out, ", ")
	}
	seed := func() string {
		t.Helper()
		if _, err := entrants.Import(srv.Eng(), t.Context(), scope, entrants.Source{Kind: entrants.SourceTroikas, Fresh: true}, nil); err != nil {
			t.Fatal(err)
		}
		result, err := entrants.Import(srv.Eng(), t.Context(), scope, entrants.Source{Kind: entrants.SourcePlayers, Fresh: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return names(result.View)
	}
	if got := seed(); got != "Ромашка, Поздняя, Лютик, Василёк, По коням" {
		t.Fatalf("seed: %s, want Ромашка ahead of Поздняя (both all Астра, applied first)", got)
	}

	// Поздняя's application was the first after all: the host moves it.
	var late int64
	for _, tr := range troikas() {
		if tr.Name == "Поздняя" {
			late = tr.ID
		}
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPut, fmt.Sprintf("/api/fest/%d/troikas/%d", festID, late),
		map[string]any{"name": "Поздняя", "players": []string{"Анна Аннова", "Бэла Бэлова"}, "applied": 1}, token); resp.Code != http.StatusOK {
		t.Fatalf("move the application: %d %s", resp.Code, resp.Body.String())
	}
	if got := order(troikas()); got != "1 Поздняя; 2 Ромашка; 3 Лютик; 4 Василёк; 5 По коням" {
		t.Fatalf("troikas after the move: %s", got)
	}
	// The Game still holds Ромашка ahead of Поздняя from the last seeding; the
	// application decides the tie, not that order.
	reseeded, err := entrants.Import(srv.Eng(), t.Context(), scope, entrants.Source{Kind: entrants.SourcePlayers, Fresh: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(reseeded.View); got != "Поздняя, Ромашка, Лютик, Василёк, По коням" {
		t.Fatalf("seed after the move: %s, want Поздняя first by its application", got)
	}
	view, err := entrants.Import(srv.Eng(), t.Context(), scope, entrants.Source{Kind: entrants.SourceTroikas, Fresh: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(view.View); got != "Поздняя, Ромашка, Лютик, Василёк, По коням" {
		t.Fatalf("list from the troikas: %s, want the order of applications", got)
	}
	if got := seed(); got != "Поздняя, Ромашка, Лютик, Василёк, По коням" {
		t.Fatalf("seed after the move: %s, want Поздняя first", got)
	}
}

// The Троечка regulations seed on the Вопросики after their first two tours,
// whatever has been played since. tours.<game> keeps a source ОД to those
// tours: its places are the table after them alone.
func TestTroikaPlayersSeedReadsTheOdAfterSomeTours(t *testing.T) {
	srv, festID, token, createGame := troikaSeedFest(t)
	db := srv.Eng().DB
	people := map[string][2]string{
		"Астра": {"Иван Иванов", "Пётр Петров"}, "Берёза": {"Сидор Сидоров", "Фома Фомин"},
		"Вяз": {"Лука Лукин", "Марк Марков"}, "По коням": {"Олег Олегов", "Глеб Глебов"},
	}
	for team, pair := range people {
		var teamID int64
		if err := db.QueryRow(`select id from fest_teams where fest_id = ? and name = ?`, festID, team).Scan(&teamID); err != nil {
			t.Fatal(err)
		}
		for i, full := range pair {
			first, last, _ := strings.Cut(full, " ")
			res, err := db.Exec(`insert into fest_players(fest_id, first_name, last_name) values(?, ?, ?)`, festID, first, last)
			if err != nil {
				t.Fatal(err)
			}
			playerID, _ := res.LastInsertId()
			if _, err := db.Exec(`insert into fest_team_players(team_id, player_id, roster_order) values(?, ?, ?)`, teamID, playerID, i); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Two tours of two. Астра (1) takes the first tour whole, По коням (4) the
	// second and one of the first: По коням wins the ОД, Астра led after tour 1.
	odID := createGame(url.Values{"game_type": {"od"}, "od_tours": {"2"}, "od_questions": {"2"}})
	var odCode string
	if err := db.QueryRow(`select code from games where id = ?`, odID).Scan(&odCode); err != nil {
		t.Fatal(err)
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, odID),
		map[string]any{"ops": []map[string]any{
			{"path": []any{"entries"}, "value": [][]int{{1, 4}, {1, 2}, {4, 3}, {4}}},
			{"path": []any{"completed"}, "value": []bool{true, true, true, true}},
		}}, token); resp.Code != http.StatusOK {
		t.Fatalf("od state: %d %s", resp.Code, resp.Body.String())
	}
	first := func(tours string) string {
		t.Helper()
		dsl := fmt.Sprintf("[init]\nseed: players\ngames: [%s]\n%splayer.p: place1\nseed.mean: mean(p)\nsorting: [mean asc]\n\n"+
			"[scheme]\nkind: roundrobin\ngroup_size: 4\nthemes: 6\nmetric: total\npoints: [1, 0.5, 0]\n", odCode, tours)
		gameID := createGame(url.Values{"game_type": {"troika"}, "troika_dsl": {dsl}})
		scope := core.FestScope{FestID: festID, GameID: gameID}
		if _, err := entrants.Import(srv.Eng(), t.Context(), scope, entrants.Source{Kind: entrants.SourceTroikas}, nil); err != nil {
			t.Fatal(err)
		}
		result, err := entrants.Import(srv.Eng(), t.Context(), scope, entrants.Source{Kind: entrants.SourcePlayers}, nil)
		if err != nil {
			t.Fatalf("seed with %q: %v", tours, err)
		}
		return result.View.Rows[0].Name
	}
	if got := first(""); got != "По коням" {
		t.Fatalf("the whole ОД seeds %s first, want По коням", got)
	}
	if got := first(fmt.Sprintf("tours.%s: 1\n", odCode)); got != "Ромашка" {
		t.Fatalf("the ОД after tour 1 seeds %s first, want Ромашка (Астра's players)", got)
	}
	if got := first(fmt.Sprintf("tours.%s: [2]\n", odCode)); got != "По коням" {
		t.Fatalf("the ОД's tour 2 alone seeds %s first, want По коням", got)
	}
}
