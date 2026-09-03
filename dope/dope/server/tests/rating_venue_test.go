package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dope/dope/domain/core"
	"dope/dope/domain/ratingvenues"
	"dope/dope/platform/realtime"
	dopeserver "dope/dope/server"

	"pecheny.me/dopecore/session"
)

// ratingSite is the venue catalogue dope's copy is taken from.
func ratingSite(t *testing.T, body string) *ratingvenues.Catalogue {
	t.Helper()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/venues") {
			t.Errorf("unexpected path %s", r.URL)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(site.Close)
	return &ratingvenues.Catalogue{HTTP: site.Client(), Base: site.URL}
}

func ratingServer(t *testing.T, body string) *dopeserver.Server {
	t.Helper()
	return dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = venueTestDB(t)
		e.RT = realtime.NewManager()
		e.Rating = ratingSite(t, body)
	})
}

// The suggest a Площадка is created from: rating.chgk.info's venues by id, by
// name or by town.
func TestRatingVenueSuggest(t *testing.T) {
	srv := ratingServer(t, `[
		{"id":3152,"name":"Тбилиси","town":{"name":"Тбилиси"}},
		{"id":3541,"name":"Больбес","town":{"name":"Москва"}}]`)
	_, token := createAPITestSession(t, srv, "rating-suggest")
	for _, c := range []struct{ query, want string }{
		{"тбилиси", `"id":3152`},
		{"москва", `"id":3541`},
		{"3541", `"id":3541`},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/rating/venues?q="+c.query, nil)
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		rec := httptest.NewRecorder()
		srv.HandleScopedAPI(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s: %d %s", c.query, rec.Code, rec.Body.String())
		}
		var found []ratingvenues.Venue
		if err := json.Unmarshal(rec.Body.Bytes(), &found); err != nil || len(found) != 1 {
			t.Errorf("%s: %v %+v", c.query, err, found)
		}
	}
}

// A site that does not answer is said so, rather than an empty catalogue.
func TestRatingVenueSuggestWithoutTheSite(t *testing.T) {
	srv := ratingServer(t, `not json`)
	_, token := createAPITestSession(t, srv, "rating-broken")
	req := httptest.NewRequest(http.MethodGet, "/api/rating/venues?q=тбилиси", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	rec := httptest.NewRecorder()
	srv.HandleScopedAPI(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "не ответил") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

// Without a session there is no catalogue at all.
func TestRatingVenueSuggestNeedsASession(t *testing.T) {
	srv := ratingServer(t, `[]`)
	rec := httptest.NewRecorder()
	srv.HandleScopedAPI(rec, httptest.NewRequest(http.MethodGet, "/api/rating/venues", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("%d", rec.Code)
	}
}
