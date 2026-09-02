package tests

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dope/dope/domain/core"
	"dope/dope/platform/realtime"
	dopeserver "dope/dope/server"
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
