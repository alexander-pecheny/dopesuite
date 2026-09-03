package tests

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dope/dope/domain/core"
	"dope/dope/platform/realtime"
	dopeserver "dope/dope/server"

	"pecheny.me/dopecore/session"
)

// A Venue is one of rating.chgk.info's venues: the form posts its id and the
// name and the town are theirs, not the Representative's.
func TestCreateVenueTakesItsNameFromRating(t *testing.T) {
	db := venueTestDB(t)
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
		e.Rating = ratingSite(t, `[{"id":3152,"name":"Тбилиси","town":{"name":"Тбилиси"}}]`)
	})
	token := createTestSession(t, srv, newVenueUser(t, db, "venue-maker"))
	post := func(form url.Values) int {
		req := httptest.NewRequest(http.MethodPost, "/host/venue", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		rec := httptest.NewRecorder()
		srv.HostPageServer().HandleHostRouter(rec, req)
		return rec.Code
	}

	if code := post(url.Values{"rating_venue_id": {"3152"}, "slug": {"tbilisi"}}); code != http.StatusSeeOther {
		t.Fatalf("create = %d", code)
	}
	var title, city string
	var ratingID int64
	if err := db.QueryRow(`select title, city, rating_venue_id from fests where kind = 'venue'`).
		Scan(&title, &city, &ratingID); err != nil {
		t.Fatal(err)
	}
	if title != "Тбилиси" || city != "Тбилиси" || ratingID != 3152 {
		t.Fatalf("%q %q %d", title, city, ratingID)
	}

	// A venue nobody picked, and one the site does not have, make nothing.
	for _, form := range []url.Values{{}, {"rating_venue_id": {"777"}}} {
		if code := post(form); code != http.StatusOK {
			t.Fatalf("refused create = %d", code)
		}
	}
	var made int
	if err := db.QueryRow(`select count(*) from fests where kind = 'venue'`).Scan(&made); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	if made != 1 {
		t.Fatalf("%d venues", made)
	}
}
