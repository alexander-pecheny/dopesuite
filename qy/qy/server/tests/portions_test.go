package tests

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// The admins read a portion as the jury will, fix a question in place, and
// mark the portion checked; only then, and only from its day, does the jury
// read it.
func TestTheAdminsReadAPortionBeforeTheJury(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	w.post(w.user("alice"), "/2026/nominate", slots("101", map[int]string{
		1: pasted("Вопрос про альфу.", "альфа", "Алиса"),
		2: pasted("Вопрос про бету.", "бета", "Алиса"),
		3: pasted("Вопрос про гамму.", "гамма", "Алиса"),
	}), http.StatusSeeOther)
	if page := w.get(w.admin, "/admin/contest/2026/portions", http.StatusOK); !strings.Contains(page, "Вопросам ещё не розданы номера") {
		t.Error("before the deal the page should say there are no portions")
	}
	w.ts.SetNow(day(t, "2026-11-24", 12))
	w.post(w.admin, "/admin/contest/2026/deal", nil, http.StatusSeeOther)
	w.ts.Exec(`update contests set portion_size = 2`)
	w.post(w.admin, "/admin/contest/2026/jurors", url.Values{"name": {"ex1"}, "telegram": {"ex1"}, "kind": {"expert"}}, http.StatusSeeOther)
	ex := w.user("ex1")

	// Portion 1 holds QY1 and QY2; one of them is rejected.
	qy1 := strconv.Itoa(w.count(`select id from questions where qy = 1`))
	qy2 := strconv.Itoa(w.count(`select id from questions where qy = 2`))
	w.ts.Exec(`update questions set status = 'rejected' where qy = 2`)
	page := w.get(w.admin, "/admin/contest/2026/portions?portion=1", http.StatusOK)
	if !strings.Contains(page, `id="QY1"`) || !strings.Contains(page, "Отклонён, жюри его не увидит") || !strings.Contains(page, "/admin/question/"+qy1+"/jury-copy") {
		t.Error("the portion should show its questions as the jury will, the rejected one marked, each with its editor")
	}
	if !strings.Contains(page, "data-toggle-answers") || !strings.Contains(page, `<details data-part="answer">`) {
		t.Error("the portion should have the button that shows and hides every answer")
	}

	// A fix in place goes back to the question on the portion's page.
	res := w.admin.Post("/admin/question/"+qy1+"/jury-copy", url.Values{"portion": {"1"}, "text": {"Исправленный вопрос."}, "answer": {"ответ"}, "author": {"Алиса"}})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/contest/2026/portions?portion=1&done=saved#QY1" {
		t.Fatalf("saving from the portion: %d %s", res.Code, res.Header().Get("Location"))
	}
	if w.count(`select count(*) from questions where id = ? and json_extract(clean, '$.text') = 'Исправленный вопрос.' and json_extract(orig, '$.author') = 'Алиса'`, qy1) != 1 {
		t.Error("the jury's copy should be saved, the author with the credits")
	}

	if strings.Contains(w.get(ex, "/2026/vote", http.StatusOK), "Исправленный вопрос.") {
		t.Error("the jury should not read a portion nobody has checked")
	}
	w.post(w.admin, "/admin/contest/2026/portions/1/check", nil, http.StatusSeeOther)
	if w.count(`select count(*) from questions where id = ? and status = 'approved'`, qy1) != 1 || w.count(`select count(*) from questions where id = ? and status = 'rejected'`, qy2) != 1 {
		t.Error("checking should approve the portion's questions and leave the rejected one rejected")
	}
	vote := w.get(ex, "/2026/vote", http.StatusOK)
	if !strings.Contains(vote, "Исправленный вопрос.") || strings.Count(vote, `name="like"`) != 1 {
		t.Error("the jury should read the checked portion, without the rejected question")
	}
	if !strings.Contains(vote, "data-toggle-answers") {
		t.Error("the jury's portion should have the button that shows and hides every answer")
	}

	// Portion 2 checked before its day stays closed until the day comes.
	w.ts.Exec(`update questions set clean = json_set(clean, '$.text', 'Третий вопрос.') where qy = 3`)
	w.post(w.admin, "/admin/contest/2026/portions/2/check", nil, http.StatusSeeOther)
	if strings.Contains(w.get(ex, "/2026/vote?portion=2", http.StatusOK), "Третий вопрос.") {
		t.Error("a portion checked early should wait for its day")
	}

	// Taking the mark back closes the portion again.
	w.post(w.admin, "/admin/contest/2026/portions/1/uncheck", nil, http.StatusSeeOther)
	if strings.Contains(w.get(ex, "/2026/vote", http.StatusOK), "Исправленный вопрос.") {
		t.Error("an unchecked portion should close to the jury")
	}
}
