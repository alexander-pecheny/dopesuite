package sameorigin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllowed(t *testing.T) {
	const trusted = "https://dope.pecheny.kz, dope.pecheny.test ,"
	cases := []struct {
		name    string
		method  string
		origin  string
		referer string
		fwdHost string
		want    bool
	}{
		{name: "a GET is never checked", method: "GET", origin: "https://evil.example", want: true},
		{name: "HEAD neither", method: "HEAD", origin: "https://evil.example", want: true},
		{name: "OPTIONS neither", method: "OPTIONS", origin: "https://evil.example", want: true},
		{name: "own host", method: "POST", origin: "https://dope.pecheny.me", want: true},
		{name: "own host in another case", method: "POST", origin: "https://DOPE.pecheny.me", want: true},
		{name: "no Origin passes", method: "POST", want: true},
		{name: "no Origin passes whatever the Referer says", method: "POST", referer: "https://evil.example/x", want: true},
		{name: "foreign Origin", method: "POST", origin: "https://evil.example", want: false},
		{name: "foreign Origin on DELETE", method: "DELETE", origin: "https://evil.example", want: false},
		{name: "another port is another origin", method: "POST", origin: "https://dope.pecheny.me:8443", want: false},
		{name: "an alias given as a URL", method: "POST", origin: "https://dope.pecheny.kz", want: true},
		{name: "an alias given bare", method: "PUT", origin: "https://dope.pecheny.test", want: true},
		{name: "a forwarded host does not vouch for its Origin", method: "POST", origin: "https://dope.pecheny.ru", fwdHost: "dope.pecheny.ru", want: false},
		{name: "a forwarded host does not vouch for a foreign Origin", method: "POST", origin: "https://evil.example", fwdHost: "dope.pecheny.kz", want: false},
		{name: "an Origin with no host", method: "POST", origin: "null", want: false},
		{name: "an Origin that is not a URL", method: "POST", origin: "http://[::1", want: false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "https://dope.pecheny.me/api/x", nil)
		r.Host = "dope.pecheny.me"
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if c.referer != "" {
			r.Header.Set("Referer", c.referer)
		}
		if c.fwdHost != "" {
			r.Header.Set("X-Forwarded-Host", c.fwdHost)
		}
		if got := Allowed(r, trusted); got != c.want {
			t.Errorf("%s: Allowed = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMiddleware(t *testing.T) {
	trusted := ""
	h := Middleware(func() string { return trusted }, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	serve := func() int {
		r := httptest.NewRequest("POST", "/x", nil)
		r.Host = "xy.pecheny.me"
		r.Header.Set("Origin", "https://xy.pecheny.ru")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if got := serve(); got != http.StatusForbidden {
		t.Fatalf("an untrusted mirror: %d, want 403", got)
	}
	trusted = "xy.pecheny.kz,xy.pecheny.ru"
	if got := serve(); got != http.StatusNoContent {
		t.Fatalf("a trusted mirror: %d, want 204", got)
	}
}
