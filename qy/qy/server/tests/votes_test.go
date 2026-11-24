package tests

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// The admins see what each juror of either jury liked and made their
// favourite, and a juror who has not voted yet with nothing.
func TestTheAdminsSeeHowEachJurorVoted(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	w.post(w.user("alice"), "/2026/nominate", slots("101", map[int]string{
		1: pasted("Вопрос про альфу.", "альфа", "Алиса"),
		2: pasted("Вопрос про бету.", "бета", "Алиса"),
	}), http.StatusSeeOther)
	w.ts.SetNow(day(t, "2026-11-24", 12))
	w.post(w.admin, "/admin/contest/2026/deal", nil, http.StatusSeeOther)
	w.ts.Exec(`update questions set published = 1`)
	w.post(w.admin, "/admin/contest/2026/portions/1/check", nil, http.StatusSeeOther)
	for _, j := range []struct{ name, handle, kind string }{
		{"Эксперт Один", "ex1", "expert"}, {"Народ Один", "pe1", "people"}, {"Эксперт Молчун", "silent", "expert"},
	} {
		w.post(w.admin, "/admin/contest/2026/jurors", url.Values{"name": {j.name}, "telegram": {j.handle}, "kind": {j.kind}}, http.StatusSeeOther)
	}
	alpha, beta := strconv.FormatInt(w.questionID("альфа"), 10), strconv.FormatInt(w.questionID("бета"), 10)
	ex1 := w.user("ex1")
	w.post(ex1, "/2026/vote", url.Values{"portion": {"1"}, "like": {alpha, beta}}, http.StatusSeeOther)
	w.post(ex1, "/2026/vote", url.Values{"portion": {"liked"}, "like": {alpha, beta}, "favourite": {beta}}, http.StatusSeeOther)
	w.post(w.user("pe1"), "/2026/vote", url.Values{"portion": {"1"}, "like": {alpha}}, http.StatusSeeOther)

	page := w.get(w.admin, "/admin/contest/2026/votes", http.StatusOK)
	qy := func(id string) string {
		return "QY" + strconv.Itoa(w.count(`select qy from questions where id = ?`, id))
	}
	for _, want := range []string{"Экспертное жюри", "Народное жюри", "Эксперт Молчун", qy(alpha), qy(beta)} {
		if !strings.Contains(page, want) {
			t.Errorf("the votes page lacks %q", want)
		}
	}
	ex := page[strings.Index(page, "Эксперт Один"):]
	ex = ex[:strings.Index(ex, "</tr>")]
	if !strings.Contains(ex, ">2<") || !strings.Contains(ex, qy(beta)) {
		t.Errorf("the expert's row should count two likes and name the favourite: %s", ex)
	}
	pe := page[strings.Index(page, "Народ Один"):]
	pe = pe[:strings.Index(pe, "</tr>")]
	if !strings.Contains(pe, ">1<") || !strings.Contains(pe, "Порция 1:") || !strings.Contains(pe, qy(alpha)+" (альфа)</a>") || strings.Contains(pe, qy(beta)) {
		t.Errorf("the people's juror's row should hold their one like: %s", pe)
	}
	silent := page[strings.Index(page, "Эксперт Молчун"):]
	silent = silent[:strings.Index(silent, "</tr>")]
	if !strings.Contains(silent, ">0<") {
		t.Errorf("a juror who has not voted should be there with nothing: %s", silent)
	}
	if strings.Contains(page, "Финал") {
		t.Error("the final's table should wait for a mark")
	}
	w.ts.Exec(`insert into scores(user_id, question_id, score, updated_at) values((select id from users where username = 'ex1'), ?, 7, '2026-12-31')`, beta)
	if page := w.get(w.admin, "/admin/contest/2026/votes", http.StatusOK); !strings.Contains(page, "Финал") || !strings.Contains(page, qy(beta)+" (бета): 7") {
		t.Error("the final's table should show the expert's mark")
	}
}

// The votes page narrows the first stage to one portion: a juror's likes
// from the others drop out, their favourite stays.
func TestTheVotesPageShowsOnePortion(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	w.post(w.user("alice"), "/2026/nominate", slots("101", map[int]string{
		1: pasted("Вопрос про альфу.", "альфа", "Алиса"),
		2: pasted("Вопрос про бету.", "бета", "Алиса"),
	}), http.StatusSeeOther)
	w.ts.Exec(`update contests set portion_size = 1`)
	w.ts.SetNow(day(t, "2026-11-24", 12))
	w.post(w.admin, "/admin/contest/2026/deal", nil, http.StatusSeeOther)
	w.post(w.admin, "/admin/contest/2026/jurors", url.Values{"name": {"Эксперт Один"}, "telegram": {"ex1"}, "kind": {"expert"}}, http.StatusSeeOther)
	w.user("ex1")
	w.ts.Exec(`insert into likes(user_id, question_id, favourite, created_at)
  select u.id, q.id, q.qy = 2, '2026-11-24' from users u, questions q where u.username = 'ex1'`)
	answer := func(qy int) string {
		var a string
		w.ts.DB().QueryRow(`select json_extract(clean, '$.answer') from questions where qy = ?`, qy).Scan(&a)
		return strings.TrimRight(a, ". ")
	}
	page := w.get(w.admin, "/admin/contest/2026/votes?portion=1", http.StatusOK)
	if !strings.Contains(page, "Все порции") || !strings.Contains(page, "QY1 ("+answer(1)+")") {
		t.Fatalf("portion 1 should be there with its tabs: %s", page)
	}
	if strings.Contains(page, "Порция 2:") || !strings.Contains(page, "QY2 ("+answer(2)+")") {
		t.Error("portion 2's likes should drop out, the favourite from it should stay")
	}
	if all := w.get(w.admin, "/admin/contest/2026/votes", http.StatusOK); !strings.Contains(all, "Порция 2:") {
		t.Error("all the portions should show by default")
	}
}
