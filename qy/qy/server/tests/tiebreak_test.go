package tests

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// The shortlist has exactly as many places as the rules give each jury. When
// questions tie at the cut, the expert jury marks them from 1 to 10 in a
// tie-break round during the factcheck, and the shortlist cannot be frozen
// until the round settles the tie.

func TestATieAtTheCutGoesToATieBreakRound(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	answers := []string{"альфа", "бета", "гамма"}
	texts := map[int]string{}
	for i, a := range answers {
		texts[i+1] = pasted("Вопрос про "+a+".", a, "Алиса")
	}
	w.post(w.user("alice"), "/2026/nominate", slots("101", texts), http.StatusSeeOther)
	w.ts.SetNow(day(t, "2026-11-24", 12))
	w.post(w.admin, "/admin/contest/2026/deal", nil, http.StatusSeeOther)
	w.ts.Exec(`update questions set status = 'approved'`)
	w.ts.Exec(`update contests set expert_cutoff = 2, people_cutoff = 0`)
	w.post(w.admin, "/admin/contest/2026/portions/1/check", nil, http.StatusSeeOther)
	for _, h := range []string{"ex1", "ex2"} {
		w.post(w.admin, "/admin/contest/2026/jurors", url.Values{"name": {h}, "telegram": {h}, "kind": {"expert"}}, http.StatusSeeOther)
	}
	ex1, ex2 := w.user("ex1"), w.user("ex2")
	id := map[string]string{}
	for _, a := range answers {
		id[a] = strconv.FormatInt(w.questionID(a), 10)
	}
	// альфа is through; бета and гамма tie for the last place.
	w.post(ex1, "/2026/vote", url.Values{"portion": {"1"}, "like": {id["альфа"], id["бета"]}}, http.StatusSeeOther)
	w.post(ex2, "/2026/vote", url.Values{"portion": {"1"}, "like": {id["альфа"], id["гамма"]}}, http.StatusSeeOther)

	w.ts.SetNow(day(t, "2027-01-02", 12))
	report := w.get(w.admin, "/admin/contest/2026/stage1", http.StatusOK)
	if strings.Count(report, "Ничья, дополнительный тур") != 2 || !strings.Contains(report, "Ничья пока не разрешена") {
		t.Error("the report should show the tie and that it is open")
	}
	w.post(w.admin, "/admin/contest/2026/freeze", nil, http.StatusBadRequest)

	page := w.get(ex1, "/2026/vote", http.StatusOK)
	if strings.Count(page, `name="mark`+id["бета"]+`"`) != 10 || strings.Contains(page, `name="mark`+id["альфа"]+`"`) {
		t.Error("the expert should mark the two tied questions, and only them")
	}
	mark := func(b, g int) url.Values {
		return url.Values{"mark" + id["бета"]: {strconv.Itoa(b)}, "mark" + id["гамма"]: {strconv.Itoa(g)}, "mark" + id["альфа"]: {"10"}}
	}
	w.post(ex1, "/2026/vote/tiebreak", mark(4, 9), http.StatusSeeOther)
	if n := w.count(`select count(*) from tiebreak_scores where question_id = ?`, id["альфа"]); n != 0 {
		t.Error("a mark for a question that is not tied was saved")
	}
	w.post(ex2, "/2026/vote/tiebreak", url.Values{"mark" + id["бета"]: {"5"}}, http.StatusSeeOther)
	// ex2's ballot leaves гамма unmarked, so only ex1's counts, and гамма wins.
	report = w.get(w.admin, "/admin/contest/2026/stage1", http.StatusOK)
	if !strings.Contains(report, "1 из 2") || !strings.Contains(report, "Ничья разрешена") ||
		!strings.Contains(report, "По экспертному жюри, после дополнительного тура") {
		t.Error("the report should count ex1's ballot and settle the tie")
	}
	w.post(w.admin, "/admin/contest/2026/freeze", nil, http.StatusSeeOther)
	if n := w.count(`select count(*) from shortlist`); n != 2 {
		t.Fatalf("shortlist: %d questions, want 2", n)
	}
	if n := w.count(`select count(*) from shortlist where question_id = ?`, id["гамма"]); n != 1 {
		t.Error("гамма won the round and should be on the shortlist")
	}
	if page := w.get(ex1, "/2026/vote", http.StatusOK); strings.Contains(page, `name="mark`) {
		t.Error("a frozen shortlist has no tie-break round")
	}
	w.post(w.user("pe"), "/2026/vote/tiebreak", nil, http.StatusForbidden)
}
