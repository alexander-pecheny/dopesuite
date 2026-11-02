package tests

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"dope/dope/domain/entrants"
	"dope/dope/domain/festops"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/multifest"
	"dope/dope/domain/ratingexport"
	"dope/dope/domain/ratingsync"
	"dope/dope/domain/roster"
	"dope/dope/platform/roles"
)

// The export compares each team with the site and with what the two last
// agreed on (ADR-0036). A roster a representative typed on the site after the
// import stays there and comes into dope; a roster both sides changed waits
// for a person's choice; зачёты dope added go by the site's ids.
func TestTheExportKeepsWhatOnlyTheSiteChanged(t *testing.T) {
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
	brest, minsk := multifest.RatingVenue{ID: 20, Request: 200, Title: "Брест"}, multifest.RatingVenue{ID: 10, Request: 100, Title: "Минск"}
	teams := []roster.FestRosterImportTeam{
		{RatingID: 3, Name: "Гамма", Venue: brest, Players: []roster.FestRosterImportPlayer{{RatingID: 30, FirstName: "Гамма", LastName: "Капитан"}},
			Flags: []roster.FestRosterFlag{{RatingID: 59, Short: "ЧР", Full: "Выводящий зачёт ЧР"}}},
		{RatingID: 5, Name: "Эпсилон", Venue: minsk},
	}
	if _, err := entrants.ImportFestRoster(srv.Eng(), t.Context(), festID, 555, teams, imports.RosterChoice{}); err != nil {
		t.Fatal(err)
	}
	withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, _, err := festops.CreateGameTx(ctx, tx, gamebuild.Spec{FestID: festID, Type: games.OD, Label: "ОД", TourComp: []int{2}})
		return err
	})
	// After the import a representative typed Гамма's real roster on the
	// site, and Эпсилон's roster changed on both sides.
	site.rosters = map[int64][]int64{3: {30, 31}, 5: {51}}
	site.requests = map[int64]int64{3: 200, 5: 100}
	site.masks = map[int64]string{}
	// The site never had Гамма's зачёт: dope added it after the import.
	if _, err := db.Exec(`update rating_sync_base set value = case fact when 'roster' then '[52]' else '[]' end
where fest_id = ? and (rating_team = 5 and fact = 'roster' or rating_team = 3 and fact = 'flags')`, festID); err != nil {
		t.Fatal(err)
	}
	adminID, token := createAPITestSession(t, srv, "boss")
	addAPITestRole(t, srv, festID, adminID, roles.Admin)
	if resp := scopedAPIRequest(t, srv, http.MethodPut, "/api/auth/rating-token", map[string]string{"token": "rt_secret"}, token); resp.Code != http.StatusOK {
		t.Fatalf("save the token: %d %s", resp.Code, resp.Body)
	}
	exportURL := fmt.Sprintf("/api/fest/%d/rating-export", festID)
	type answer struct {
		Plan   ratingexport.Plan    `json:"plan"`
		Report *ratingexport.Report `json:"report"`
	}
	plan := decodeInto[answer](t, scopedAPIRequest(t, srv, http.MethodGet, exportURL, nil, token)).Plan
	actions := map[string]ratingsync.Action{}
	for _, f := range plan.Facts {
		actions[f.Key] = f.Action
	}
	if actions["3.roster"] != ratingsync.Take || actions["5.roster"] != ratingsync.Ask || actions["3.flags"] != ratingsync.Send || plan.Undecided != 1 {
		t.Fatalf("the facts are %+v, undecided %d", plan.Facts, plan.Undecided)
	}
	for _, v := range plan.Venues {
		if len(v.Teams) != 0 {
			t.Fatalf("a roster would go: %+v", v)
		}
	}
	if len(plan.Flags) != 1 || !slices.Equal(plan.Flags[0].IDs, []int64{59}) {
		t.Fatalf("the зачёты are %+v", plan.Flags)
	}

	if resp := scopedAPIRequest(t, srv, http.MethodPost, exportURL, nil, token); resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "Выберите версию") {
		t.Fatalf("a send with a choice left: %d %s", resp.Code, resp.Body)
	}
	if len(site.posted) != 0 {
		t.Fatalf("a send with a choice left wrote %v", site.posted)
	}
	sent := decodeInto[answer](t, scopedAPIRequest(t, srv, http.MethodPost, exportURL, map[string]any{"choices": map[string]string{"5.roster": "site"}}, token))
	if sent.Report == nil || slices.Contains(site.posted, "/tournaments/555/results_entry/rosters") || !slices.Contains(site.posted, "/tournaments/555/results_entry/flags") {
		t.Fatalf("sent %v, report %+v", site.posted, sent.Report)
	}
	if !slices.Equal(site.rosters[3], []int64{30, 31}) {
		t.Fatalf("the site's Гамма is %v", site.rosters[3])
	}
	fest := map[string][]int64{}
	rows, err := db.Query(`select t.name, coalesce(p.rating_id, 0) from fest_team_players tp join fest_players p on p.id = tp.player_id
join fest_teams t on t.id = tp.team_id where t.fest_id = ? order by tp.roster_order`, festID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		var id int64
		rows.Scan(&name, &id)
		fest[name] = append(fest[name], id)
	}
	rows.Close()
	if !slices.Equal(fest["Гамма"], []int64{30, 31}) || !slices.Equal(fest["Эпсилон"], []int64{51}) {
		t.Fatalf("the fest took %v", fest)
	}
	again := decodeInto[answer](t, scopedAPIRequest(t, srv, http.MethodGet, exportURL, nil, token)).Plan
	if len(again.Facts) != 0 {
		t.Fatalf("after the export the two still differ: %+v", again.Facts)
	}
}
