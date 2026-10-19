package tests

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"qy/qy/domain/contest"
	"qy/qy/domain/question"
	"qy/qy/platform/gq"
	qyserver "qy/qy/server"
)

// One contest, end to end, the way its people meet it: nomination, review,
// numbers, the first stage, the shortlist, the final vote and the results.

func day(t *testing.T, d string, hour int) time.Time {
	t.Helper()
	at, ok := contest.Day(d)
	if !ok {
		t.Fatalf("bad day %s", d)
	}
	return at.Add(time.Duration(hour) * time.Hour)
}

type world struct {
	t     *testing.T
	ts    *qyserver.TestServer
	admin *qyserver.Client
}

func newWorld(t *testing.T) *world {
	ts := qyserver.NewTestServer(t, t.TempDir())
	id := ts.AddUser("org")
	ts.Exec(`insert into admins(telegram_username) values('ORG')`)
	w := &world{t: t, ts: ts, admin: ts.As(id)}
	w.post(w.admin, "/admin/contests", nil, http.StatusSeeOther)
	return w
}

func (w *world) post(c *qyserver.Client, target string, form url.Values, want int) string {
	w.t.Helper()
	res := c.Post(target, form)
	if res.Code != want {
		w.t.Fatalf("POST %s = %d, want %d: %s", target, res.Code, want, res.Body.String())
	}
	return res.Body.String()
}

func (w *world) get(c *qyserver.Client, target string, want int) string {
	w.t.Helper()
	res := c.Get(target)
	if res.Code != want {
		w.t.Fatalf("GET %s = %d, want %d: %s", target, res.Code, want, res.Body.String())
	}
	return res.Body.String()
}

func (w *world) user(handle string) *qyserver.Client { return w.ts.As(w.ts.AddUser(handle)) }

func (w *world) count(query string, args ...any) int {
	w.t.Helper()
	var n int
	if err := w.ts.DB().QueryRow(query, args...).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

func (w *world) questionID(answer string) int64 {
	w.t.Helper()
	var id int64
	if err := w.ts.DB().QueryRow(`select id from questions where json_extract(orig, '$.answer') = ?`, answer).Scan(&id); err != nil {
		w.t.Fatalf("question with answer %q: %v", answer, err)
	}
	return id
}

func pasted(text, answer, author string) string {
	return "Вопрос 1.\n" + text + "\nОтвет: " + answer + "\nКомментарий: так вышло.\nАвтор: " + author
}

func slots(rating string, texts map[int]string) url.Values {
	f := url.Values{"rating_id": {rating}}
	for i, t := range texts {
		f.Set("slot"+strconv.Itoa(i), t)
	}
	return f
}

func TestTheNewContestStartsFromLastYearsDates(t *testing.T) {
	w := newWorld(t)
	body := w.get(w.admin, "/admin/contest/2026", http.StatusOK)
	for _, want := range []string{"2026-11-09", "2026-11-23", "2027-01-25", "2025-11-01"} {
		if !strings.Contains(body, want) {
			t.Errorf("settings lack %s", want)
		}
	}
	if w.count(`select count(*) from pages where path = '2026'`) != 1 {
		t.Error("the year page was not seeded")
	}
}

func TestNominationIsOpenOnlyDuringItsStage(t *testing.T) {
	w := newWorld(t)
	alice := w.user("alice")
	w.ts.SetNow(day(t, "2026-11-08", 12))
	if body := w.get(alice, "/nominate", http.StatusOK); strings.Contains(body, `name="slot1"`) {
		t.Error("the form is open before the stage")
	}
	w.post(alice, "/nominate", slots("1", map[int]string{1: pasted("Текст.", "ответ", "Кто-то")}), http.StatusBadRequest)
	w.ts.SetNow(day(t, "2026-11-09", 0))
	if body := w.get(alice, "/nominate", http.StatusOK); !strings.Contains(body, `name="slot8"`) {
		t.Error("the form should have five slots and three reserves")
	}
	w.post(alice, "/nominate", slots("abc", map[int]string{1: pasted("Текст.", "ответ", "Кто-то")}), http.StatusBadRequest)
}

func TestAContestEndToEnd(t *testing.T) {
	w := newWorld(t)
	w.ts.SetGotQuestions(gq.Fixed{777: {Question: question.Question{
		Text: "Опубликованный вопрос про ИКС.", Answer: "икс", Author: "Пётр Петров",
	}, Tournament: "Кубок"}})
	w.ts.SetModel(&qyserver.Model{Reply: `{"suggestions": [{"field": "comment", "quote": "так вышло", "replacement": "", "reason": "подпись"}]}`})

	// ---- nomination ----
	w.ts.SetNow(day(t, "2026-11-10", 12))
	alice, bob := w.user("alice"), w.user("bob")
	w.post(alice, "/nominate", slots("101", map[int]string{
		1: pasted("Вчера Эдуард Голуб ел яблоко и думал об искусстве.", "искусство", "Эдуард Голуб (Москва)"),
		2: "https://gotquestions.online/question/777",
		6: pasted("Запасной вопрос Алисы.", "запас алисы", "Алиса"),
	}), http.StatusSeeOther)
	// Bob's first pick is the question Alice already nominated by link, so his
	// reserve counts in its place.
	w.post(bob, "/nominate", slots("202", map[int]string{
		1: "https://gotquestions.online/question/777",
		6: pasted("Запасной вопрос Боба.", "запас боба", "Боб"),
	}), http.StatusSeeOther)
	if body := w.get(alice, "/nominate", http.StatusOK); !strings.Contains(body, "искусство") {
		t.Error("the form does not show what was parsed")
	}
	if w.count(`select count(*) from questions`) != 4 {
		t.Fatalf("two nominations of one link should share a question")
	}

	// ---- review ----
	w.ts.ReviewPending()
	art := w.questionID("искусство")
	if n := w.count(`select count(*) from suggestions where question_id = ? and source = 'rule' and quote = 'Эдуард Голуб'`, art); n != 1 {
		t.Fatalf("rule suggestions: %d", n)
	}
	if n := w.count(`select count(*) from suggestions where question_id = ? and source = 'ai'`, art); n != 1 {
		t.Fatalf("model suggestions: %d", n)
	}
	var sid int64
	w.ts.DB().QueryRow(`select id from suggestions where question_id = ? and source = 'rule'`, art).Scan(&sid)
	href := "/admin/question/" + strconv.FormatInt(art, 10)
	w.post(w.admin, href+"/suggestions/"+strconv.FormatInt(sid, 10), url.Values{"decision": {"accept"}}, http.StatusSeeOther)
	page := w.get(w.admin, href, http.StatusOK)
	if !strings.Contains(page, "Вчера [автор вопроса] ел яблоко") {
		t.Error("the accepted redaction is not in the jury's copy")
	}

	// ---- numbers ----
	w.post(w.admin, "/admin/contest/2026/deal", nil, http.StatusBadRequest)
	w.ts.SetNow(day(t, "2026-11-24", 12))
	w.post(w.admin, "/admin/contest/2026/deal", nil, http.StatusSeeOther)
	// Bob's reserve stands in for his duplicate; Alice's for a main slot she
	// left empty.
	if n := w.count(`select count(*) from questions where qy is not null`); n != 4 {
		t.Fatalf("numbered %d questions, want four", n)
	}
	w.post(w.admin, "/admin/contest/2026/deal", nil, http.StatusBadRequest)
	for _, a := range []string{"искусство", "икс", "запас боба"} {
		id := w.questionID(a)
		form := url.Values{"status": {"approved"}, "text": {"Текст " + a}, "answer": {a}, "pin": {""}}
		if a == "икс" {
			form.Set("published", "1")
			form.Set("author", "Пётр Петров")
		}
		w.post(w.admin, "/admin/question/"+strconv.FormatInt(id, 10), form, http.StatusSeeOther)
	}

	// ---- the first stage ----
	w.post(w.admin, "/admin/contest/2026/jurors", url.Values{"name": {"Эксперт Один"}, "telegram": {"@Ex1"}, "kind": {"expert"}}, http.StatusSeeOther)
	w.post(w.admin, "/admin/contest/2026/jurors", url.Values{"name": {"Народ Один"}, "telegram": {"pe1"}, "kind": {"people"}}, http.StatusSeeOther)
	ex, pe := w.user("ex1"), w.user("pe1")
	if body := w.get(alice, "/vote", http.StatusOK); strings.Contains(body, `name="like"`) {
		t.Error("a non-juror sees the likes")
	}
	expertPage := w.get(ex, "/vote", http.StatusOK)
	if strings.Count(expertPage, `name="like"`) != 3 {
		t.Errorf("the expert should see all three questions")
	}
	if strings.Contains(expertPage, "Голуб") {
		t.Error("the jury can see the author")
	}
	if peoplePage := w.get(pe, "/vote", http.StatusOK); strings.Count(peoplePage, `name="like"`) != 1 {
		t.Errorf("the people's jury should see only the published question")
	}
	likes := url.Values{"portion": {"1"}}
	for _, a := range []string{"искусство", "икс"} {
		likes.Add("like", strconv.FormatInt(w.questionID(a), 10))
	}
	w.post(ex, "/vote", likes, http.StatusSeeOther)
	w.post(pe, "/vote", url.Values{"portion": {"1"}, "like": {strconv.FormatInt(w.questionID("запас боба"), 10)}}, http.StatusSeeOther)
	if n := w.count(`select count(*) from likes`); n != 2 {
		t.Errorf("likes: %d; the people's juror's like of an unpublished question should be ignored", n)
	}

	// ---- the factcheck: a like can be taken back, not given ----
	w.ts.SetNow(day(t, "2027-01-02", 12))
	w.post(ex, "/vote", url.Values{"like": {strconv.FormatInt(w.questionID("икс"), 10), strconv.FormatInt(w.questionID("запас боба"), 10)}}, http.StatusSeeOther)
	if n := w.count(`select count(*) from likes`); n != 1 {
		t.Errorf("likes after the factcheck: %d", n)
	}
	report := w.get(w.admin, "/admin/contest/2026/stage1", http.StatusOK)
	if !strings.Contains(report, "По экспертному жюри") {
		t.Error("the report does not say why a question is on the shortlist")
	}
	w.post(w.admin, "/admin/contest/2026/freeze", nil, http.StatusSeeOther)
	if n := w.count(`select count(*) from shortlist`); n != 1 {
		t.Fatalf("shortlist: %d", n)
	}

	// ---- the final ----
	w.ts.SetNow(day(t, "2027-01-20", 12))
	x := strconv.FormatInt(w.questionID("икс"), 10)
	w.post(ex, "/final", url.Values{"score" + x: {"9"}}, http.StatusSeeOther)
	w.post(alice, "/popular", url.Values{"rating_id": {"101"}, "pick0": {x}, "pick1": {x}, "pick2": {x}}, http.StatusBadRequest)
	if body := w.get(w.admin, "/2026/shortlist/", http.StatusOK); !strings.Contains(body, "Текст икс") {
		t.Error("the public shortlist does not show its question")
	}

	// ---- the results ----
	w.get(w.admin, "/2026/results/", http.StatusNotFound)
	w.ts.SetNow(day(t, "2027-01-25", 12))
	final := w.get(w.admin, "/admin/contest/2026/final", http.StatusOK)
	if !strings.Contains(final, "1 из 1") {
		t.Errorf("the expert's ballot should be complete")
	}
	w.post(w.admin, "/admin/contest/2026/publish", nil, http.StatusSeeOther)
	results := w.get(alice, "/2026/results/", http.StatusOK)
	for _, want := range []string{"Пётр Петров", "Место у жюри: 1", "Текст икс"} {
		if !strings.Contains(results, want) {
			t.Errorf("the results lack %q", want)
		}
	}
}

func TestPopularVoteNeedsThreeDifferentQuestions(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	alice := w.user("alice")
	texts := map[int]string{}
	for i, a := range []string{"один", "два", "три", "четыре"} {
		texts[i+1] = pasted("Вопрос "+a+".", a, "Автор "+a)
	}
	w.post(alice, "/nominate", slots("1", texts), http.StatusSeeOther)
	w.ts.SetNow(day(t, "2026-11-24", 12))
	w.post(w.admin, "/admin/contest/2026/deal", nil, http.StatusSeeOther)
	var ids []string
	for _, a := range []string{"один", "два", "три", "четыре"} {
		id := strconv.FormatInt(w.questionID(a), 10)
		ids = append(ids, id)
		w.post(w.admin, "/admin/question/"+id, url.Values{"status": {"approved"}, "text": {"Т " + a}, "answer": {a}, "pin": {"add"}}, http.StatusSeeOther)
	}
	w.ts.SetNow(day(t, "2027-01-02", 12))
	w.post(w.admin, "/admin/contest/2026/freeze", nil, http.StatusSeeOther)
	w.ts.SetNow(day(t, "2027-01-20", 12))
	w.post(alice, "/popular", url.Values{"rating_id": {"1"}, "pick0": {ids[0]}, "pick1": {ids[1]}}, http.StatusBadRequest)
	w.post(alice, "/popular", url.Values{"rating_id": {"1"}, "pick0": {ids[0]}, "pick1": {ids[1]}, "pick2": {ids[2]}}, http.StatusSeeOther)
	w.post(alice, "/popular", url.Values{"rating_id": {"1"}, "pick0": {ids[3]}, "pick1": {ids[1]}, "pick2": {ids[2]}}, http.StatusSeeOther)
	if n := w.count(`select count(*) from popular_votes`); n != 1 {
		t.Errorf("a voter's second vote should replace the first, got %d votes", n)
	}
}
