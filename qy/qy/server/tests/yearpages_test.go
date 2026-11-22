package tests

import (
	"net/http"
	"strings"
	"testing"
)

// The jury's pages live under the contest's year, like the nomination form,
// and only the current year's exist.
func TestTheJuryPagesAreUnderTheYear(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-24", 12))
	alice := w.user("alice")
	for _, page := range []string{"vote", "final", "popular"} {
		w.get(alice, "/2026/"+page, http.StatusOK)
		w.get(alice, "/2025/"+page, http.StatusNotFound)
		w.get(alice, "/"+page, http.StatusNotFound)
	}
	ex := w.user("ex1")
	w.post(w.admin, "/admin/contest/2026/jurors", map[string][]string{"name": {"ex1"}, "telegram": {"ex1"}, "kind": {"expert"}}, http.StatusSeeOther)
	if page := w.get(ex, "/2026/vote", http.StatusOK); strings.Contains(page, `"/vote`) {
		t.Error("the vote page should link only to the year's addresses")
	}
}
