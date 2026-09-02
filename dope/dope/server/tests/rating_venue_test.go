package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dope/dope/domain/core"
	"dope/dope/platform/realtime"
	dopeserver "dope/dope/server"

	"pecheny.me/dopecore/session"
)

type stubRoundTripper func(*http.Request) *http.Response

func (f stubRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r), nil }

func ratingResponse(t *testing.T, status int, body string) *http.Client {
	t.Helper()
	return &http.Client{Transport: stubRoundTripper(func(r *http.Request) *http.Response {
		if !strings.HasPrefix(r.URL.String(), "https://api.rating.chgk.net/venues/") {
			t.Errorf("unexpected url %s", r.URL)
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{},
		}
	})}
}

// The one live rating.chgk.info call: the venue's name and the town it is in.
func TestRatingVenueLookup(t *testing.T) {
	db := venueTestDB(t)
	cases := []struct {
		name   string
		status int
		body   string
		code   int
		want   string
	}{
		{"ok", 200, `{"id":6826,"name":"Санкт-Петербург / Трубников Артём","town":{"name":"Санкт-Петербург"}}`, 200,
			`{"name":"Санкт-Петербург / Трубников Артём","city":"Санкт-Петербург"}`},
		{"missing", 404, `{}`, 400, "такой площадки нет"},
		{"broken", 500, ``, 400, "не ответил"},
		{"garbage", 200, `not json`, 400, "не ответил"},
	}
	for _, c := range cases {
		srv := dopeserver.NewTestServer(func(e *core.Engine) {
			e.DB = db
			e.RT = realtime.NewManager()
		})
		srv.RatingHTTP = ratingResponse(t, c.status, c.body)
		userID, token := createAPITestSession(t, srv, "rating-"+c.name)
		_ = userID
		req := httptest.NewRequest(http.MethodGet, "/api/rating/venue/6826", nil)
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		rec := httptest.NewRecorder()
		srv.HandleScopedAPI(rec, req)
		if rec.Code != c.code {
			t.Errorf("%s: %d, want %d (%s)", c.name, rec.Code, c.code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s: body %q, want %q", c.name, rec.Body.String(), c.want)
		}
		if c.code == 200 {
			var venue dopeserver.RatingVenue
			if err := json.Unmarshal(rec.Body.Bytes(), &venue); err != nil || venue.City == "" {
				t.Errorf("%s: %v %+v", c.name, err, venue)
			}
		}
	}
}

// Without a session there is no lookup at all.
func TestRatingVenueNeedsASession(t *testing.T) {
	db := venueTestDB(t)
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	srv.RatingHTTP = ratingResponse(t, 200, `{}`)
	rec := httptest.NewRecorder()
	srv.HandleScopedAPI(rec, httptest.NewRequest(http.MethodGet, "/api/rating/venue/1", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("%d", rec.Code)
	}
}
