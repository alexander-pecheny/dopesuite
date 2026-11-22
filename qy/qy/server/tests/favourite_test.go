package tests

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The first stage's page: every portion has a tab, the ones not open yet
// disabled; after them, the questions the juror liked, where one of them can
// be their favourite, worth three points instead of one.

// expertPoints is the expert jury's points for the question with answer a on
// the admins' first-stage report.
func expertPoints(t *testing.T, w *world, a string) string {
	t.Helper()
	page := w.get(w.admin, "/admin/contest/2026/stage1", http.StatusOK)
	m := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(a) + `.*?<td[^>]*>(\d+)</td>`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no row for %s on the report", a)
	}
	return m[1]
}

func TestAJurorsFavouriteIsWorthThreeLikes(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	w.post(w.user("alice"), "/2026/nominate", slots("101", map[int]string{
		1: pasted("Вопрос про альфу.", "альфа", "Алиса"),
		2: pasted("Вопрос про бету.", "бета", "Алиса"),
		3: pasted("Вопрос про гамму.", "гамма", "Алиса"),
	}), http.StatusSeeOther)
	w.ts.SetNow(day(t, "2026-11-24", 12))
	w.post(w.admin, "/admin/contest/2026/deal", nil, http.StatusSeeOther)
	w.ts.Exec(`update questions set status = 'approved'`)
	w.ts.Exec(`update contests set portion_size = 2`)
	for _, h := range []string{"ex1", "ex2"} {
		w.post(w.admin, "/admin/contest/2026/jurors", url.Values{"name": {h}, "telegram": {h}, "kind": {"expert"}}, http.StatusSeeOther)
	}
	ex1, ex2 := w.user("ex1"), w.user("ex2")

	page := w.get(ex1, "/2026/vote", http.StatusOK)
	if !regexp.MustCompile(`<button[^>]*disabled[^>]*>2</button>`).MatchString(page) || !strings.Contains(page, "Понравившиеся") {
		t.Error("the second portion should be a disabled tab, followed by the liked one")
	}
	var first []string
	for _, a := range []string{"альфа", "бета", "гамма"} {
		var qy int
		w.ts.DB().QueryRow(`select coalesce(qy, 0) from questions where id = ?`, w.questionID(a)).Scan(&qy)
		if qy <= 2 {
			first = append(first, a)
		}
	}
	if len(first) != 2 {
		t.Fatalf("the first portion holds %v", first)
	}
	a, b := first[0], first[1]
	ida, idb := strconv.FormatInt(w.questionID(a), 10), strconv.FormatInt(w.questionID(b), 10)
	w.post(ex1, "/2026/vote", url.Values{"portion": {"1"}, "like": {ida, idb}}, http.StatusSeeOther)
	w.post(ex2, "/2026/vote", url.Values{"portion": {"1"}, "like": {ida}}, http.StatusSeeOther)

	liked := w.get(ex1, "/2026/vote?portion=liked", http.StatusOK)
	if strings.Count(liked, `name="favourite"`) != 3 {
		t.Fatal("the liked tab should offer both liked questions and no favourite")
	}
	w.post(ex1, "/2026/vote", url.Values{"portion": {"liked"}, "like": {ida, idb}, "favourite": {idb}}, http.StatusSeeOther)
	if pa, pb := expertPoints(t, w, a), expertPoints(t, w, b); pa != "2" || pb != "3" {
		t.Errorf("points: %s %s, %s %s; want 2 and 3", a, pa, b, pb)
	}

	// Unliking the favourite takes its points; a favourite must be liked.
	w.post(ex1, "/2026/vote", url.Values{"portion": {"liked"}, "like": {ida}, "favourite": {idb}}, http.StatusSeeOther)
	if n := w.count(`select count(*) from likes where favourite = 1`); n != 0 {
		t.Errorf("a favourite the juror no longer likes: %d", n)
	}
	w.post(ex1, "/2026/vote", url.Values{"portion": {"liked"}, "like": {ida}, "favourite": {ida}}, http.StatusSeeOther)
	w.post(ex1, "/2026/vote", url.Values{"portion": {"liked"}, "like": {ida}, "favourite": {""}}, http.StatusSeeOther)
	if pa := expertPoints(t, w, a); pa != "2" {
		t.Errorf("after the favourite is cleared, %s has %s points", a, pa)
	}
}
