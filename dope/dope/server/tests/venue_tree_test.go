package tests

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"dope/dope/domain/core"
	"dope/dope/domain/venues"
	"dope/dope/platform/realtime"
	"dope/dope/platform/util"
	dopeserver "dope/dope/server"

	"pecheny.me/dopecore/session"
)

// A Venue's host pages live under /host/venue and a fest's under /host/fest;
// each tree turns the other kind away rather than serving it twice.
func TestHostTreesAreKeptApart(t *testing.T) {
	db := venueTestDB(t)
	_, slot := newVenueSlot(t, db, []int{2})
	now := "2026-09-02T00:00:00Z"
	if _, err := db.Exec(`
insert into fests(id, slug, title, description, kind, created_by, revision, created_at, updated_at, is_public)
values(900, 'kubok', 'Кубок', '', 'fest', null, 1, ?, ?, 1)`, now, now); err != nil {
		t.Fatal(err)
	}
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	venueRef := "1"
	if err := db.QueryRow(`select id from fests where kind = 'venue'`).Scan(&venueRef); err != nil {
		t.Fatal(err)
	}
	_ = slot

	cases := []struct {
		path     string
		code     int
		location string
	}{
		{"/host/fest/" + venueRef, http.StatusMovedPermanently, "/host/venue/" + venueRef},
		{"/host/fest/" + venueRef + "/game/1", http.StatusMovedPermanently, "/host/venue/" + venueRef + "/game/1"},
		{"/host/fest/" + venueRef + "/game/1/", http.StatusMovedPermanently, "/host/venue/" + venueRef + "/game/1/"},
		{"/host/venue/kubok", http.StatusNotFound, ""},
		{"/host/venue/kubok/numbers", http.StatusNotFound, ""},
		// An ordinary fest is untouched: no session, so the host policy sends
		// it back to /host rather than redirecting trees.
		{"/host/fest/kubok", http.StatusSeeOther, "/host"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		srv.HostPageServer().HandleHostRouter(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != c.code {
			t.Errorf("%s: %d, want %d", c.path, rec.Code, c.code)
		}
		if c.location != "" && rec.Header().Get("Location") != c.location {
			t.Errorf("%s: Location %q, want %q", c.path, rec.Header().Get("Location"), c.location)
		}
	}
}

// Deleting an игра takes its Slot with it — the applications, their versions and the
// Game all hang off the same row — and lands the Representative on the Venue,
// not on the /host/fest tree a Venue is never served from.
func TestDeletingAVenueGameTakesTheSlot(t *testing.T) {
	db := venueTestDB(t)
	festID, slot := newVenueSlot(t, db, []int{2})
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	applicant := newVenueUser(t, db, "applicant")
	fileApplication(t, db, slot, applicant, "Мантисса", 0, nil)

	userID := newVenueUser(t, db, "organizer")
	if _, err := db.Exec(`insert into fest_organizers(fest_id, user_id, role, added_at) values(?, ?, 'creator', ?)`,
		festID, userID, util.UtcNow()); err != nil {
		t.Fatal(err)
	}
	ref := strconv.FormatInt(festID, 10)
	req := httptest.NewRequest(http.MethodPost,
		"/host/venue/"+ref+"/game/"+slot.GameRef()+"/delete", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: createTestSession(t, srv, userID)})
	rec := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d: %s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), "/host/venue/"+ref; got != want {
		t.Errorf("Location %q, want %q", got, want)
	}
	for _, q := range []string{
		`select count(*) from games where id = ?`,
		`select count(*) from slots where game_id = ?`,
		`select count(*) from slot_applications where slot_id in (select id from slots where game_id = ?)`,
	} {
		var n int
		if err := db.QueryRow(q, slot.GameID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s left %d rows", q, n)
		}
	}
}

// The registration opens and shuts through its own button, and the save beside
// it — which says nothing about the state — leaves the state where it was.
func TestRegistrationOpensAndShutsByButton(t *testing.T) {
	db := venueTestDB(t)
	festID, slot := newVenueSlot(t, db, []int{2})
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	userID := newVenueUser(t, db, "organizer")
	if _, err := db.Exec(`insert into fest_organizers(fest_id, user_id, role, added_at) values(?, ?, 'creator', ?)`,
		festID, userID, util.UtcNow()); err != nil {
		t.Fatal(err)
	}
	cookie := createTestSession(t, srv, userID)
	path := "/host/venue/" + strconv.FormatInt(festID, 10) + "/game/" + slot.GameRef() + "/reg"
	post := func(form url.Values) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: cookie})
		rec := httptest.NewRecorder()
		srv.HostPageServer().HandleHostRouter(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%s = %d: %s", form.Encode(), rec.Code, rec.Body.String())
		}
	}
	state := func() venues.Slot {
		t.Helper()
		got, err := venues.LoadSlot(t.Context(), db, slot.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	post(url.Values{"reg": {"open"}, "link_visible": {"1"}})
	if got := state(); got.RegClosed || !got.LinkVisible {
		t.Fatalf("open left closed=%v visible=%v", got.RegClosed, got.LinkVisible)
	}
	post(url.Values{"reg_opens_at": {"2026-09-04 19:00"}})
	if got := state(); got.RegClosed || got.RegOpensAt != "2026-09-04 19:00" || got.LinkVisible {
		t.Fatalf("save changed the state: %+v", got)
	}
	post(url.Values{"reg": {"close"}})
	if !state().RegClosed {
		t.Error("close left the registration open")
	}
}

// The viewer side moves with it: a Venue's Game is watched under /venue/.
func TestViewerVenueGameRedirect(t *testing.T) {
	db := venueTestDB(t)
	festID, _ := newVenueSlot(t, db, []int{2})
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	rec := httptest.NewRecorder()
	srv.HandleFestRouter(rec, httptest.NewRequest(http.MethodGet, "/fest/1/game/1/", nil))
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/venue/1/game/1/" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Location"))
	}
	_ = festID
}

// A form on a shared page posts to the tree it was rendered under, and the
// router refuses to redirect a write: a 301 would replay it as a GET and drop
// it. The venue's own tree assigns the numbers.
func TestVenueNumbersFormPostsToItsOwnTree(t *testing.T) {
	db := venueTestDB(t)
	festID, _ := newVenueSlot(t, db, []int{2})
	now := util.UtcNow()
	for i, name := range []string{"Астра", "Берёза"} {
		if _, err := db.Exec(`insert into fest_teams(fest_id, name, city, position) values(?, ?, '', ?)`,
			festID, name, i+1); err != nil {
			t.Fatal(err)
		}
	}
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	userID := newVenueUser(t, db, "organizer")
	if _, err := db.Exec(`insert into fest_organizers(fest_id, user_id, role, added_at) values(?, ?, 'creator', ?)`,
		festID, userID, now); err != nil {
		t.Fatal(err)
	}
	token := createTestSession(t, srv, userID)

	post := func(path string) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(""))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		rec := httptest.NewRecorder()
		srv.HostPageServer().HandleHostRouter(rec, req)
		return rec.Code
	}
	ref := strconv.FormatInt(festID, 10)
	if code := post("/host/fest/" + ref + "/numbers/auto"); code != http.StatusMethodNotAllowed {
		t.Fatalf("POST to the wrong tree = %d, want 405", code)
	}
	var numbered int
	if err := db.QueryRow(`select count(*) from fest_teams where fest_id = ? and number is not null`, festID).Scan(&numbered); err != nil {
		t.Fatal(err)
	}
	if numbered != 0 {
		t.Fatalf("a refused POST numbered %d teams", numbered)
	}
	if code := post("/host/venue/" + ref + "/numbers/auto"); code != http.StatusOK {
		t.Fatalf("POST to the venue tree = %d, want 200", code)
	}
	if err := db.QueryRow(`select count(*) from fest_teams where fest_id = ? and number is not null`, festID).Scan(&numbered); err != nil {
		t.Fatal(err)
	}
	if numbered != 2 {
		t.Fatalf("numbered %d teams, want 2", numbered)
	}
}

// The viewer tree is the Venue's alone: an ordinary fest's game is not served
// under /venue/.
func TestViewerVenueTreeRefusesAPlainFest(t *testing.T) {
	db := venueTestDB(t)
	newVenueSlot(t, db, []int{2})
	now := util.UtcNow()
	if _, err := db.Exec(`
insert into fests(id, slug, title, description, kind, created_by, revision, created_at, updated_at, is_public)
values(900, 'kubok', 'Кубок', '', 'fest', null, 1, ?, ?, 1)`, now, now); err != nil {
		t.Fatal(err)
	}
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	rec := httptest.NewRecorder()
	srv.HandleVenueGameRouter(rec, httptest.NewRequest(http.MethodGet, "/venue/kubok/game/1/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("/venue/kubok/game/1/ = %d, want 404", rec.Code)
	}
}

// The reg token is the invitation, and a Representative hands it out from the
// game's own page: the public landing says a registration is open and stops
// there, for a Representative as much as for a stranger.
func TestVenueLandingKeepsTheRegToken(t *testing.T) {
	db := venueTestDB(t)
	festID, _ := newVenueSlot(t, db, []int{2})
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	ref := strconv.FormatInt(festID, 10)

	get := func(token string) string {
		req := httptest.NewRequest(http.MethodGet, "/venue/"+ref, nil)
		if token != "" {
			req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		}
		rec := httptest.NewRecorder()
		srv.HostPageServer().HandleVenueRouter(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("landing = %d", rec.Code)
		}
		return rec.Body.String()
	}

	anon := get("")
	if strings.Contains(anon, "/reg/") {
		t.Error("the anonymous landing leaks the reg token")
	}
	if !strings.Contains(anon, "открыта") {
		t.Error("the anonymous landing should still say the registration is open")
	}

	userID := newVenueUser(t, db, "organizer")
	if _, err := db.Exec(`insert into fest_organizers(fest_id, user_id, role, added_at) values(?, ?, 'creator', ?)`,
		festID, userID, util.UtcNow()); err != nil {
		t.Fatal(err)
	}
	if member := get(createTestSession(t, srv, userID)); strings.Contains(member, "/reg/") {
		t.Error("the landing hands a Representative the reg token")
	}
}

// The roster is asked for after the application is accepted, so a POST that carries
// one before then is not believed.
func TestPendingApplicationStoresNoRoster(t *testing.T) {
	db := venueTestDB(t)
	_, slot := newVenueSlot(t, db, []int{2})
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	userID := newVenueUser(t, db, "applicant")
	token := createTestSession(t, srv, userID)

	post := func(roster string) {
		form := url.Values{"team_name": {"Мантисса"}, "rating_team_id": {"0"}, "roster_json": {roster}}
		req := httptest.NewRequest(http.MethodPost, "/reg/"+slot.RegToken, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		rec := httptest.NewRecorder()
		srv.HostPageServer().HandleVenueRouter(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("submit = %d: %s", rec.Code, rec.Body.String())
		}
	}
	filed := `[{"player_id":1,"surname":"Иванов","name":"Иван","captain":true}]`

	post(filed)
	app, err := venues.UserApplication(t.Context(), db, slot.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(app.Roster) != 0 {
		t.Fatalf("a pending заявка kept a roster: %+v", app.Roster)
	}

	setStatus(t, db, slot, userID, venues.StatusAccepted)
	post(filed)
	app, err = venues.UserApplication(t.Context(), db, slot.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(app.Roster) != 1 || app.Roster[0].Surname != "Иванов" {
		t.Fatalf("an accepted заявка should keep its roster: %+v", app.Roster)
	}
}
