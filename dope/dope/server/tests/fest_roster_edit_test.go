package tests

import (
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"dope/dope/domain/imports"
	"dope/dope/domain/roster"
)

type editTeam struct {
	ID       int64  `json:"id"`
	RatingID int64  `json:"rating_id"`
	Name     string `json:"name"`
	City     string `json:"city"`
	Flags    string `json:"flags"`
	Hand     bool   `json:"hand"`
	Edited   bool   `json:"edited"`
}

type editPlayer struct {
	RatingID  int64  `json:"rating_id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// festRosterNow is the fest roster as «team: people» lines, sorted: what a
// person reading the teams page sees.
func festRosterNow(t *testing.T, game *serverGame) []string {
	t.Helper()
	rows, err := game.db().Query(`
select t.name, coalesce(group_concat(p.first_name, ','), '')
from fest_teams t
left join (select ftp.team_id, p.first_name from fest_team_players ftp join fest_players p on p.id = ftp.player_id
           order by ftp.team_id, ftp.roster_order) p on p.team_id = t.id
where t.fest_id = ? and t.deleted = 0
group by t.id`, game.festID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name, people string
		if err := rows.Scan(&name, &people); err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(people, ",")
		sort.Strings(parts)
		out = append(out, name+": "+strings.Join(parts, ","))
	}
	sort.Strings(out)
	return out
}

func siteTeams(teams map[int64][]string) []roster.FestRosterImportTeam {
	names := map[int64]string{1: "Бобры", 2: "Зубры", 3: "Совы"}
	var out []roster.FestRosterImportTeam
	for id, people := range teams {
		team := roster.FestRosterImportTeam{RatingID: id, Name: names[id], City: "Брест"}
		for i, p := range people {
			team.Players = append(team.Players, roster.FestRosterImportPlayer{RatingID: id*100 + int64(i+1), FirstName: p})
		}
		out = append(out, team)
	}
	return out
}

// The fest roster is the import plus the host's edits (ADR-0024). The host
// imports, fixes the roster on the spot — moves a player, adds one the site
// does not know, adds a team, renames one, types a Flag — and imports again
// to pick up a late registration. Every fix survives, the late player
// arrives, the preview says so first, and the import can be undone.
func TestHostRosterEditsSurviveAReimport(t *testing.T) {
	srv := newAuthTestServer(t)
	srv.SetEditBatchWindow(time.Millisecond)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	festID := newFest(t, db, "local", "Местный фест", systemUserID(t, db))
	game := &serverGame{t: t, srv: srv, festID: festID, token: token}
	api := func(method, path string, body any) (int, string) {
		t.Helper()
		resp := scopedAPIRequest(t, srv, method, fmt.Sprintf("/api/fest/%d%s", festID, path), body, token)
		return resp.Code, resp.Body.String()
	}
	idOf := func(name string) int64 {
		t.Helper()
		var id int64
		if err := db.QueryRow(`select id from fest_teams where fest_id = ? and name = ? and deleted = 0`, festID, name).Scan(&id); err != nil {
			t.Fatalf("team %s: %v", name, err)
		}
		return id
	}

	// The site's roster.
	site := map[int64][]string{1: {"Аня", "Борис"}, 2: {"Вера", "Глеб"}}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, siteTeams(site), imports.RosterChoice{}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// The host's fixes: Борис moves to Зубры, Дина (unknown to the site) joins
	// them, Бобры are renamed and get a Flag, and a team is made by hand.
	bobry, zubry := idOf("Бобры"), idOf("Зубры")
	if code, body := api(http.MethodPut, fmt.Sprintf("/teams/%d", zubry), map[string]any{"name": "Зубры", "city": "Брест",
		"players": []editPlayer{{201, "Вера", ""}, {202, "Глеб", ""}, {102, "Борис", ""}, {0, "Дина", "Новикова"}}}); code != http.StatusOK {
		t.Fatalf("edit Зубры: %d %s", code, body)
	}
	if code, body := api(http.MethodPut, fmt.Sprintf("/teams/%d", bobry), map[string]any{"name": "Бобры-2", "city": "Брест",
		"players": []editPlayer{{101, "Аня", ""}}}); code != http.StatusOK {
		t.Fatalf("edit Бобры: %d %s", code, body)
	}
	if code, body := api(http.MethodPatch, "/teams/flags", map[string]any{"flags": map[string]string{fmt.Sprint(bobry): "Студ"}}); code != http.StatusOK {
		t.Fatalf("flags: %d %s", code, body)
	}
	code, body := api(http.MethodPost, "/teams", map[string]any{"name": "Сборная", "players": []editPlayer{{0, "Жора", ""}}})
	if code != http.StatusOK {
		t.Fatalf("new team: %d %s", code, body)
	}
	edited := []string{"Бобры-2: Аня", "Зубры: Борис,Вера,Глеб,Дина", "Сборная: Жора"}
	if got := festRosterNow(t, game); !reflect.DeepEqual(got, edited) {
		t.Fatalf("after the edits: %v, want %v", got, edited)
	}
	var handNumber int64
	if err := db.QueryRow(`select number from fest_teams where id = ?`, idOf("Сборная")).Scan(&handNumber); err != nil || handNumber != 3 {
		t.Fatalf("hand team number = %d (%v), want the next free, 3", handNumber, err)
	}

	// A late registration on the site: Ева for Бобры. The preview says what
	// the import does and writes nothing.
	site[1] = append(site[1], "Ева")
	result, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, siteTeams(site), imports.RosterChoice{Preview: true})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	plan := result.Plan
	if len(plan.Players) != 1 || plan.Players[0].Team != "Бобры-2 (Брест)" || !reflect.DeepEqual(plan.Players[0].Added, []string{"Ева"}) || len(plan.Players[0].Removed) != 0 {
		t.Fatalf("plan players = %+v", plan.Players)
	}
	if len(plan.AddedTeams)+len(plan.DroppedTeams)+len(plan.Renamed)+len(plan.Conflicts) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Kept.HandTeams != 1 || plan.Kept.Renamed != 1 || plan.Kept.Flags != 1 {
		t.Fatalf("kept = %+v", plan.Kept)
	}
	if got := festRosterNow(t, game); !reflect.DeepEqual(got, edited) {
		t.Fatalf("a preview wrote: %v", got)
	}

	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, siteTeams(site), imports.RosterChoice{}); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	reimported := []string{"Бобры-2: Аня,Ева", "Зубры: Борис,Вера,Глеб,Дина", "Сборная: Жора"}
	if got := festRosterNow(t, game); !reflect.DeepEqual(got, reimported) {
		t.Fatalf("after the re-import: %v, want %v", got, reimported)
	}
	var flags string
	if err := db.QueryRow(`select coalesce(group_concat(short), '') from fest_team_flags where team_id = ?`, bobry).Scan(&flags); err != nil || flags != "Студ" {
		t.Fatalf("Бобры's Flags = %q (%v), want the host's", flags, err)
	}

	// Undo puts the roster back as it was before the import.
	if code, body := api(http.MethodPost, "/rating-import/undo", nil); code != http.StatusOK {
		t.Fatalf("undo: %d %s", code, body)
	}
	if got := festRosterNow(t, game); !reflect.DeepEqual(got, edited) {
		t.Fatalf("after the undo: %v, want %v", got, edited)
	}
	if code, _ := api(http.MethodPost, "/rating-import/undo", nil); code != http.StatusBadRequest {
		t.Fatalf("a second undo with nothing saved: %d, want 400", code)
	}

	// A rating team the host takes off stays off through an import.
	if code, body := api(http.MethodDelete, fmt.Sprintf("/teams/%d", zubry), nil); code != http.StatusOK {
		t.Fatalf("remove Зубры: %d %s", code, body)
	}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, siteTeams(site), imports.RosterChoice{}); err != nil {
		t.Fatalf("import after the removal: %v", err)
	}
	if got := festRosterNow(t, game); !reflect.DeepEqual(got, []string{"Бобры-2: Аня,Ева", "Сборная: Жора"}) {
		t.Fatalf("after removing Зубры and importing: %v", got)
	}

	// The team list says which teams are the host's.
	code, body = api(http.MethodGet, "/teams", nil)
	if code != http.StatusOK || !strings.Contains(body, `"hand":true`) || !strings.Contains(body, `"edited":true`) {
		t.Fatalf("teams: %d %s", code, body)
	}
}

// The site moves a player the host had placed by hand: the import asks, keeps
// the host's placement by default, and takes the site's when told to.
func TestReimportAsksWhenTheSiteMovesAHostsPlayer(t *testing.T) {
	srv := newAuthTestServer(t)
	srv.SetEditBatchWindow(time.Millisecond)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	festID := newFest(t, db, "conflict", "Фест", systemUserID(t, db))
	game := &serverGame{t: t, srv: srv, festID: festID, token: token}
	site := map[int64][]string{1: {"Аня", "Борис"}, 2: {"Вера"}, 3: {}}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, siteTeams(site), imports.RosterChoice{}); err != nil {
		t.Fatalf("import: %v", err)
	}
	var zubry int64
	if err := db.QueryRow(`select id from fest_teams where fest_id = ? and rating_id = 2`, festID).Scan(&zubry); err != nil {
		t.Fatal(err)
	}
	resp := scopedAPIRequest(t, srv, http.MethodPut, fmt.Sprintf("/api/fest/%d/teams/%d", festID, zubry),
		map[string]any{"name": "Зубры", "city": "Брест", "players": []editPlayer{{201, "Вера", ""}, {102, "Борис", ""}}}, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("move Борис: %d %s", resp.Code, resp.Body.String())
	}

	// The site now registers Борис for Совы.
	moved := siteTeams(map[int64][]string{1: {"Аня"}, 2: {"Вера"}, 3: {}})
	for i := range moved {
		if moved[i].RatingID == 3 {
			moved[i].Players = []roster.FestRosterImportPlayer{{RatingID: 102, FirstName: "Борис"}}
		}
	}
	result, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, moved, imports.RosterChoice{Preview: true})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(result.Plan.Conflicts) != 1 || result.Plan.Conflicts[0].HandTeam != "Зубры" || result.Plan.Conflicts[0].SiteTeam != "Совы" {
		t.Fatalf("conflicts = %+v", result.Plan.Conflicts)
	}
	key := result.Plan.Conflicts[0].Key

	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, moved, imports.RosterChoice{}); err != nil {
		t.Fatalf("import keeping the host's: %v", err)
	}
	if got := festRosterNow(t, game); !reflect.DeepEqual(got, []string{"Бобры: Аня", "Зубры: Борис,Вера", "Совы: "}) {
		t.Fatalf("host's placement: %v", got)
	}
	// Answered once, it is not asked again.
	again, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, moved, imports.RosterChoice{Preview: true})
	if err != nil || len(again.Plan.Conflicts) != 0 {
		t.Fatalf("asked again: %+v (%v)", again.Plan, err)
	}

	// Undo, and this time take the site's placement.
	resp = scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/rating-import/undo", festID), nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("undo: %d %s", resp.Code, resp.Body.String())
	}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, moved, imports.RosterChoice{AcceptSite: map[string]bool{key: true}}); err != nil {
		t.Fatalf("import taking the site's: %v", err)
	}
	if got := festRosterNow(t, game); !reflect.DeepEqual(got, []string{"Бобры: Аня", "Зубры: Вера", "Совы: Борис"}) {
		t.Fatalf("site's placement: %v", got)
	}
}
