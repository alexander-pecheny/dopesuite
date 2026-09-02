package tests

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"dope/dope/domain/core"
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
		{"/host/fest/" + venueRef + "/slot/1", http.StatusMovedPermanently, "/host/venue/" + venueRef + "/slot/1"},
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
