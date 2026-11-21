package tests

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"qy/qy/domain/question"
	"qy/qy/platform/gq"
	qyserver "qy/qy/server"
)

// A picture a nominator attaches: kept when the form comes back with a
// mistake, kept on a question nominated by its link, and drawn on the card
// for whoever sees the card.

var png = []byte("\x89PNG\r\n\x1a\nnot really")

// hiddenValue is what the page put in the hidden field name, as a browser
// would send it back.
func hiddenValue(t *testing.T, page, name string) string {
	t.Helper()
	input := regexp.MustCompile(`<input[^>]*name="` + name + `"[^>]*>`).FindString(page)
	m := regexp.MustCompile(`value="([^"]*)"`).FindStringSubmatch(input)
	if m == nil {
		t.Fatalf("no field %s in the page", name)
	}
	return html.UnescapeString(m[1])
}

func TestAFileSurvivesAFormSentBackWithAMistake(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	alice := w.user("alice")
	form := slots("101", map[int]string{
		1: pasted("Вопрос с картинкой.", "картинка", "Алиса"),
		2: "Вопрос 2.\nБез ответа и автора.",
	})
	res := alice.PostMultipart("/2026/nominate", form, qyserver.File{Field: "slot1_file", Name: "pic.png", Data: png})
	page := res.Body.String()
	if res.Code != http.StatusBadRequest || !strings.Contains(page, "Приложить pic.png") {
		t.Fatalf("POST = %d, and the form should offer the file it was sent", res.Code)
	}

	// The browser sends back what the page holds; the file input is empty.
	for _, f := range []string{"slot1_kept_name", "slot1_kept_type", "slot1_kept_data"} {
		form.Set(f, hiddenValue(t, page, f))
	}
	form.Set("slot1_keep_file", "1")
	for k := range form {
		if strings.HasPrefix(k, "slot2_") {
			form.Del(k)
		}
	}
	w.post(alice, "/2026/nominate", form, http.StatusSeeOther)
	var name string
	var data []byte
	if err := w.ts.DB().QueryRow(`select name, data from question_files`).Scan(&name, &data); err != nil || name != "pic.png" || string(data) != string(png) {
		t.Fatalf("the file kept over: %q, %q, %v", name, data, err)
	}

	// Unticked, it is left behind.
	w.post(alice, "/2026/nominate", url.Values{"rating_id": {"101"}}, http.StatusSeeOther)
	form.Del("slot1_keep_file")
	w.post(alice, "/2026/nominate", form, http.StatusSeeOther)
	if n := w.count(`select count(*) from question_files`); n != 0 {
		t.Errorf("an unticked file was attached: %d files", n)
	}
}

func TestAFileStaysOnAQuestionNominatedByItsLink(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	w.ts.SetGotQuestions(gq.Fixed{777: {Question: question.Question{
		Text: "Опубликованный вопрос про ИКС.", Answer: "икс", Source: "книга", Author: "Пётр Петров",
	}, Tournament: "Кубок", TournamentID: 12345}})
	alice := w.user("alice")
	form := slots("101", map[int]string{1: "https://gotquestions.online/question/777"})
	res := alice.PostMultipart("/2026/nominate", form, qyserver.File{Field: "slot1_file", Name: "pic.png", Data: png})
	if res.Code != http.StatusSeeOther || w.count(`select count(*) from question_files`) != 1 {
		t.Fatalf("POST = %d; the file should be attached to the linked question", res.Code)
	}
}

func TestAnAttachedPictureIsOnTheCardForWhoeverSeesIt(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	alice, bob := w.user("alice"), w.user("bob")
	expert, people := w.user("ex1"), w.user("pe1")
	w.post(w.admin, "/admin/contest/2026/jurors", url.Values{"name": {"Эксперт"}, "telegram": {"@ex1"}, "kind": {"expert"}}, http.StatusSeeOther)
	w.post(w.admin, "/admin/contest/2026/jurors", url.Values{"name": {"Народ"}, "telegram": {"pe1"}, "kind": {"people"}}, http.StatusSeeOther)
	form := slots("101", map[int]string{1: pasted("Вопрос с картинкой.", "картинка", "Алиса")})
	alice.PostMultipart("/2026/nominate", form,
		qyserver.File{Field: "slot1_file", Name: "pic.png", Data: png})
	id := w.questionID("картинка")
	var fid int64
	w.ts.DB().QueryRow(`select id from question_files`).Scan(&fid)
	link := "/nominate/file/" + strconv.FormatInt(fid, 10) + ".png"

	page := w.get(w.admin, "/admin/question/"+strconv.FormatInt(id, 10), http.StatusOK)
	if !strings.Contains(page, `data-images="[&#34;`+link+`&#34;]"`) {
		t.Errorf("the card should carry the picture's link %s", link)
	}
	w.get(alice, link, http.StatusOK)
	w.get(expert, link, http.StatusNotFound) // not approved yet

	w.ts.Exec(`update questions set status = 'approved' where id = ?`, id)
	w.get(expert, link, http.StatusOK)
	w.get(people, link, http.StatusNotFound) // the people's jury reads only published questions
	w.get(bob, link, http.StatusNotFound)
	if res := w.ts.Anonymous().Get(link); res.Code != http.StatusNotFound {
		t.Errorf("a visitor before the shortlist is public: %d", res.Code)
	}
}

func TestAnAdminAttachesAndRemovesAHandout(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	alice := w.user("alice")
	w.post(alice, "/2026/nominate", slots("101", map[int]string{1: pasted("Вопрос без картинки.", "картинка", "Алиса")}), http.StatusSeeOther)
	href := "/admin/question/" + strconv.FormatInt(w.questionID("картинка"), 10)
	if res := alice.PostMultipart(href+"/files", nil, qyserver.File{Field: "file", Name: "pic.png", Data: png}); res.Code == http.StatusSeeOther {
		t.Fatal("a nominator should not attach through the admin page")
	}
	if res := w.admin.PostMultipart(href+"/files", nil, qyserver.File{Field: "file", Name: "pic.png", Data: png}); res.Code != http.StatusSeeOther {
		t.Fatalf("attach = %d: %s", res.Code, res.Body.String())
	}
	var fid int64
	w.ts.DB().QueryRow(`select id from question_files`).Scan(&fid)
	page := w.get(w.admin, href, http.StatusOK)
	if !strings.Contains(page, "pic.png") || !strings.Contains(page, `/nominate/file/`+strconv.FormatInt(fid, 10)+`.png`) {
		t.Error("the page should list the file and the card draw it")
	}
	w.post(w.admin, href+"/files/"+strconv.FormatInt(fid, 10)+"/remove", nil, http.StatusSeeOther)
	if w.count(`select count(*) from question_files`) != 0 {
		t.Error("the file should be gone")
	}
}
