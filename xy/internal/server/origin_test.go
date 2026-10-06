package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The check on every write is off until XY_TRUSTED_ORIGIN_HOSTS is set, and
// then lets the mirrors through and nobody else. Serial: it sets the env.
func TestGuardOriginFollowsTheEnv(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	post := func(h http.Handler, origin string) int {
		r := httptest.NewRequest("POST", "/api/boards", nil)
		r.Host = "xy.pecheny.me"
		r.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if got := post(guardOrigin(ok), "https://evil.example"); got != http.StatusNoContent {
		t.Fatalf("unset, a foreign Origin: %d, want it served as before", got)
	}
	t.Setenv(trustedOriginHostsEnv, "xy.pecheny.kz, https://xy.pecheny.ru")
	h := guardOrigin(ok)
	cases := map[string]int{
		"https://xy.pecheny.me": http.StatusNoContent,
		"https://xy.pecheny.kz": http.StatusNoContent,
		"https://xy.pecheny.ru": http.StatusNoContent,
		"https://evil.example":  http.StatusForbidden,
	}
	for origin, want := range cases {
		if got := post(h, origin); got != want {
			t.Errorf("%s: %d, want %d", origin, got, want)
		}
	}
}
