package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"dope/dope/domain/entrants"
	"dope/dope/domain/festops"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/multifest"
	"dope/dope/domain/ratingexport"
	"dope/dope/domain/roster"
	"dope/dope/platform/roles"

	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
)

// fakeRatingSite is the rating site's «ввод» API with one synch tournament,
// 555, of two площадки: Минск (заявка 100, representative 7) and Брест
// (заявка 200, representative 8). Before any roster arrives the tournament
// has Гамма, in Минск's заявка by mistake.
type fakeRatingSite struct {
	t      *testing.T
	mu     sync.Mutex
	posted []string
	bodies []map[string]any
}

func (f *fakeRatingSite) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer rt_secret" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method == http.MethodPost {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			f.t.Errorf("%s: %v", r.URL.Path, err)
		}
		f.posted = append(f.posted, r.URL.Path)
		f.bodies = append(f.bodies, body)
		if r.URL.Path == "/tournaments/555/results_entry/players" {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id": 99, "surname": "Безымянный", "name": "Новичок"}`)
			return
		}
		fmt.Fprint(w, `{"warnings": [], "moved": [3], "request": 200}`)
		return
	}
	switch r.URL.Path {
	case "/tournaments/555":
		fmt.Fprint(w, `{"questionQty": {"1": 2}}`)
	case "/tournaments/555/requests":
		fmt.Fprint(w, `[{"id": 100, "status": "A", "venue": {"id": 10}, "representative": {"id": 7, "name": "Иван", "surname": "Минский"}},
			{"id": 300, "status": "D", "venue": {"id": 20}, "representative": {"id": 9}},
			{"id": 200, "status": "A", "venue": {"id": 20}, "representative": {"id": 8, "name": "Пётр", "surname": "Брестский"}}]`)
	case "/players":
		if r.URL.Query().Get("surname") == "Капитан" {
			fmt.Fprint(w, `[{"id": 77, "surname": "Капитан", "name": "Альфа", "patronymic": "Иванович"}]`)
			return
		}
		fmt.Fprint(w, `[{"id": 5, "surname": "Безымянная", "name": "Новичок"}]`)
	case "/tournaments/555/results_entry":
		if len(f.posted) <= 1 {
			fmt.Fprint(w, `{"teams": [{"team": 3, "name": "Гамма", "request": 100}]}`)
		} else {
			fmt.Fprint(w, `{"teams": [{"team": 1, "request": 100}, {"team": 2, "request": 100}, {"team": 3, "request": 200}]}`)
		}
	case "/seasons":
		fmt.Fprint(w, `[{"id": 60, "dateStart": "2000-01-01T00:00:00+03:00", "dateEnd": "2100-01-01T00:00:00+03:00"}]`)
	case "/teams/1/seasons":
		fmt.Fprint(w, `[{"idplayer": 10}]`)
	default:
		fmt.Fprint(w, `[]`)
	}
}

// An organizer's export sends each площадка's rosters as its representative,
// moves the team the site had in the wrong заявка first, and sends every
// team's answers at once; a player the site does not know is created there
// first, and goes in with the id the site gave.
func TestTheExportSendsEachVenuesRostersAsItsRepresentative(t *testing.T) {
	t.Parallel()
	site := &fakeRatingSite{t: t}
	fake := httptest.NewServer(site)
	t.Cleanup(fake.Close)
	srv := newAuthTestServer(t)
	srv.Eng().RatingAPI = fake.URL
	db := srv.Eng().DB
	res, err := db.Exec(`insert into fests(title, rating_id, revision, created_at, updated_at, kind) values('Синхрон', 555, 1, '2026-10-25', '2026-10-25', ?)`, multifest.Synch)
	if err != nil {
		t.Fatal(err)
	}
	festID, _ := res.LastInsertId()
	minsk, brest := multifest.RatingVenue{ID: 10, Title: "Минск"}, multifest.RatingVenue{ID: 20, Title: "Брест"}
	team := func(id int64, name string, venue multifest.RatingVenue) roster.FestRosterImportTeam {
		return roster.FestRosterImportTeam{RatingID: id, Name: name, Venue: venue,
			Players: []roster.FestRosterImportPlayer{{RatingID: id * 10, FirstName: name, LastName: "Капитан"}}}
	}
	gamma := team(3, "Гамма", brest)
	gamma.Players = append(gamma.Players, roster.FestRosterImportPlayer{FirstName: "Новичок", LastName: "Безымянный"})
	teams := []roster.FestRosterImportTeam{team(1, "Альфа", minsk), team(2, "Бета", minsk), gamma}
	if _, err := entrants.ImportFestRoster(srv.Eng(), t.Context(), festID, 555, teams, imports.RosterChoice{}); err != nil {
		t.Fatalf("import: %v", err)
	}
	var gameID, minskID int64
	db.QueryRow(`select id from multifest_venues where fest_id = ? and rating_venue_id = 10`, festID).Scan(&minskID)
	withTx(t, db, func(ctx context.Context, tx *sql.Tx) (err error) {
		gameID, _, err = festops.CreateGameTx(ctx, tx, gamebuild.Spec{FestID: festID, Type: games.OD, Label: "ОД", TourComp: []int{2}})
		return err
	})
	adminID, token := createAPITestSession(t, srv, "boss")
	addAPITestRole(t, srv, festID, adminID, roles.Admin)
	sheet := fmt.Sprint(minskID)
	resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, gameID), map[string]any{"ops": []map[string]any{
		{"op": "set", "path": []any{"venues", sheet, "entries", 0}, "value": []int{2, 0}},
		{"op": "set", "path": []any{"venues", sheet, "completed", 0}, "value": true},
	}}, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("enter Бета's answer: %d %s", resp.Code, resp.Body)
	}

	exportURL := fmt.Sprintf("/api/fest/%d/rating-export", festID)
	if resp := scopedAPIRequest(t, srv, http.MethodGet, exportURL, nil, token); resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "токен") {
		t.Fatalf("an export without a token: %d %s", resp.Code, resp.Body)
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPut, "/api/auth/rating-token", map[string]string{"token": " rt_secret "}, token); resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `"tail":"cret"`) {
		t.Fatalf("save the token: %d %s", resp.Code, resp.Body)
	}

	type answer struct {
		Plan   ratingexport.Plan    `json:"plan"`
		Report *ratingexport.Report `json:"report"`
	}
	preview := decodeInto[answer](t, scopedAPIRequest(t, srv, http.MethodGet, exportURL, nil, token))
	plan := preview.Plan
	if preview.Report != nil || len(site.posted) != 0 {
		t.Fatalf("the preview sent something: %v", site.posted)
	}
	venues := map[string]ratingexport.Venue{}
	for _, v := range plan.Venues {
		venues[v.Title] = v
	}
	if len(plan.Venues) != 2 || venues["Минск"].Representative != 7 || venues["Брест"].Representative != 8 || venues["Брест"].Request != 200 {
		t.Fatalf("the площадки are %+v", plan.Venues)
	}
	if got := venues["Минск"].Teams[0].Players[0].Flag + venues["Минск"].Teams[1].Players[0].Flag; got != "БЛ" {
		t.Fatalf("Альфа's and Бета's flags are %s", got)
	}
	if len(plan.Moves) != 1 || plan.Moves[0].Team != 3 || plan.Moves[0].Request != 200 {
		t.Fatalf("the moves are %+v", plan.Moves)
	}
	masks := map[int64]string{}
	for _, r := range plan.Results {
		masks[r.Team] = r.Mask
	}
	if masks[1] != "00" || masks[2] != "10" || masks[3] != "00" {
		t.Fatalf("the masks are %v", masks)
	}
	if len(plan.Unknown) != 1 || plan.Unknown[0].Name != "Безымянный Новичок" || len(plan.Unknown[0].Candidates) != 0 ||
		plan.Unknown[0].Default() != ratingexport.ChoiceNew || len(plan.Skipped) != 0 || len(plan.Warnings) != 2 {
		t.Fatalf("unknown %+v, skipped %+v, warnings %v", plan.Unknown, plan.Skipped, plan.Warnings)
	}

	identify := map[string]any{"choices": map[string]string{plan.Unknown[0].Ref: ratingexport.ChoiceNew}}
	identified := decodeInto[answer](t, scopedAPIRequest(t, srv, http.MethodPost, exportURL+"/identify", identify, token))
	if len(identified.Plan.Unknown) != 0 || site.posted[0] != "/tournaments/555/results_entry/players" ||
		site.bodies[0]["surname"] != "Безымянный" || site.bodies[0]["name"] != "Новичок" {
		t.Fatalf("identify posted %v %v, unknown left %+v", site.posted, site.bodies, identified.Plan.Unknown)
	}
	var gammaPlayers []int64
	for _, team := range identified.Plan.Venues {
		for _, r := range team.Teams {
			if r.Name == "Гамма" {
				for _, p := range r.Players {
					gammaPlayers = append(gammaPlayers, p.Player)
				}
			}
		}
	}
	if !slices.Contains(gammaPlayers, 99) {
		t.Fatalf("Гамма's roster after the identify is %v", gammaPlayers)
	}
	var stored int64
	db.QueryRow(`select coalesce(rating_id, 0) from fest_players where fest_id = ? and last_name = 'Безымянный'`, festID).Scan(&stored)
	if stored != 99 {
		t.Fatalf("the fest roster keeps rating id %d for the new player", stored)
	}
	site.posted, site.bodies = site.posted[1:], site.bodies[1:]

	sent := decodeInto[answer](t, scopedAPIRequest(t, srv, http.MethodPost, exportURL, nil, token))
	want := []string{"/tournaments/555/results_entry/move", "/tournaments/555/results_entry/rosters", "/tournaments/555/results_entry/rosters", "/tournaments/555/results_entry"}
	if strings.Join(site.posted, " ") != strings.Join(want, " ") || sent.Report == nil || sent.Report.Results != 3 {
		t.Fatalf("sent %v, report %+v", site.posted, sent.Report)
	}
	reps := []any{site.bodies[1]["representative"], site.bodies[2]["representative"]}
	if !slices.Contains(reps, any(float64(7))) || !slices.Contains(reps, any(float64(8))) {
		t.Fatalf("the rosters went as %v", reps)
	}

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d/rating/export", festID), nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	page := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(page, req)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Минский") {
		t.Fatalf("the page: %d", page.Code)
	}
}
