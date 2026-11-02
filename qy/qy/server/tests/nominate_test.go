package tests

import (
	"net/http"
	"strings"
	"testing"

	qyserver "qy/qy/server"
)

// The nomination form: a question published outside gotquestions, the
// required fields, the parse that saves nothing, the handout file, and the
// admin who tries the form before it opens.

func TestAQuestionPublishedOnTelegramIsTypedIn(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	alice := w.user("alice")
	form := slots("101", map[int]string{1: pasted("Вопрос из канала.", "канал", "Алиса")})
	form.Set("slot1_kind", "published")
	form.Set("slot1_link", "https://t.me/somechannel/12")
	form.Del("slot1_tournament")
	w.post(alice, "/nominate", form, http.StatusSeeOther)
	id := w.questionID("канал")
	if n := w.count(`select count(*) from questions where id = ? and published = 1 and link = 'https://t.me/somechannel/12' and gq_id is null`, id); n != 1 {
		t.Error("a telegram question should be stored as published, with its link")
	}
}

func TestAnIncompleteQuestionIsNotSaved(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	alice := w.user("alice")
	form := slots("101", map[int]string{
		1: pasted("Хороший вопрос.", "хорошо", "Алиса"),
		2: "Вопрос 2.\nБез ответа и автора.",
	})
	body := w.post(alice, "/nominate", form, http.StatusBadRequest)
	if !strings.Contains(body, "Номинация не сохранена") || !strings.Contains(body, "Заполните обязательные поля") {
		t.Error("the form should say what is missing")
	}
	if !strings.Contains(body, "Хороший вопрос.") {
		t.Error("the form shown again should keep what was typed")
	}
	if w.count(`select count(*) from nominations`) != 0 {
		t.Error("nothing should be saved while one slot is wrong")
	}
	form.Del("slot1_tournament")
	if body := w.post(alice, "/nominate", form, http.StatusBadRequest); !strings.Contains(body, "Укажите ссылку на турнир") {
		t.Error("an unpublished question needs its tournament")
	}
}

func TestParseFillsTheFieldsAndSavesNothing(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	alice := w.user("alice")
	form := slots("101", map[int]string{1: "Вопрос 1.\n[Раздаточный материал: яблоко]\nНазовите ИКС.\nОтвет: груша.\nИсточник: книга.\nАвтор: Алиса"})
	form.Set("parse", "1")
	body := w.post(alice, "/nominate", form, http.StatusOK)
	if !strings.Contains(body, ">груша.</textarea>") || !strings.Contains(body, ">яблоко</textarea>") {
		t.Error("the fields should hold the parsed answer and the handout")
	}
	if w.count(`select count(*) from nominations`) != 0 {
		t.Error("a parse saves nothing")
	}
	res := alice.PostJSON("/api/nominate/parse", `{"text": "Вопрос 1.\nНазовите ИКС.\nОтвет: груша.\nАвтор: Алиса"}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"answer":"груша."`) {
		t.Errorf("parse endpoint: %d %s", res.Code, res.Body.String())
	}
}

func TestAHandoutFileStaysWithItsQuestion(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	alice, bob := w.user("alice"), w.user("bob")
	form := slots("101", map[int]string{1: pasted("Вопрос с картинкой.", "картинка", "Алиса")})
	png := []byte("\x89PNG\r\n\x1a\nnot really")
	res := alice.PostMultipart("/nominate", form, qyserver.File{Field: "slot1_file", Name: "pic.png", Data: png})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("POST = %d: %s", res.Code, res.Body.String())
	}
	var fid string
	if err := w.ts.DB().QueryRow(`select id from question_files`).Scan(&fid); err != nil {
		t.Fatal(err)
	}
	w.get(alice, "/nominate/file/"+fid, http.StatusOK)
	w.get(w.admin, "/nominate/file/"+fid, http.StatusOK)
	w.get(bob, "/nominate/file/"+fid, http.StatusNotFound)

	// An edit makes a new question; the file comes along.
	form.Set("slot1_raw", pasted("Вопрос с картинкой, поправленный.", "картинка", "Алиса"))
	w.post(alice, "/nominate", form, http.StatusSeeOther)
	if w.count(`select count(*) from question_files f join nominations n on n.question_id = f.question_id`) != 1 {
		t.Error("the edited question lost its file")
	}
	form.Set("slot1_drop_file", "1")
	w.post(alice, "/nominate", form, http.StatusSeeOther)
	if w.count(`select count(*) from question_files`) != 0 {
		t.Error("a dropped file should be gone")
	}
}

func TestAnAdminMayTryTheFormBeforeItOpens(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-02", 12))
	if body := w.get(w.admin, "/nominate", http.StatusOK); !strings.Contains(body, `name="slot1_kind"`) {
		t.Error("an admin should see the form before the stage")
	}
	if body := w.get(w.admin, "/2026/", http.StatusOK); !strings.Contains(body, `href="/nominate"`) {
		t.Error("the year page should link the form for an admin")
	}
	if body := w.get(w.user("alice"), "/2026/", http.StatusOK); strings.Contains(body, `href="/nominate"`) {
		t.Error("the year page should not link the form for everyone yet")
	}
}
