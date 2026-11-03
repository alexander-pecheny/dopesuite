package tests

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"qy/qy/domain/question"
	"qy/qy/platform/gq"
	"qy/qy/platform/rating"
	"qy/qy/platform/telegram"
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
	if body := w.get(w.admin, "/nominate", http.StatusOK); !strings.Contains(body, `name="slot1_raw"`) {
		t.Error("an admin should see the form before the stage")
	}
	if body := w.get(w.admin, "/2026/", http.StatusOK); !strings.Contains(body, `href="/nominate"`) {
		t.Error("the year page should link the form for an admin")
	}
	if body := w.get(w.user("alice"), "/2026/", http.StatusOK); strings.Contains(body, `href="/nominate"`) {
		t.Error("the year page should not link the form for everyone yet")
	}
}

func TestAGotQuestionsLinkLoadsIntoTheFields(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	w.ts.SetGotQuestions(gq.Fixed{777: {Question: question.Question{
		Text: "[Раздаточный материал: яблоко]\nОпубликованный вопрос про ИКС.", Answer: "икс", Source: "книга", Author: "Пётр Петров",
	}, Tournament: "Кубок", TournamentID: 12345}})
	alice, bob := w.user("alice"), w.user("bob")

	res := alice.PostJSON("/api/nominate/load", `{"link": "https://gotquestions.online/question/777"}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"handout":"яблоко"`) ||
		!strings.Contains(res.Body.String(), `"tournament":"https://rating.chgk.info/tournament/12345"`) {
		t.Errorf("load endpoint: %d %s", res.Code, res.Body.String())
	}
	if res := alice.PostJSON("/api/nominate/load", `{"link": "https://gotquestions.online/question/1"}`); res.Code != http.StatusBadRequest {
		t.Errorf("an unknown question: %d", res.Code)
	}

	// Without the page's script the load button posts the form.
	form := slots("101", map[int]string{1: "https://gotquestions.online/question/777"})
	form.Set("load", "1")
	if body := w.post(alice, "/nominate", form, http.StatusOK); !strings.Contains(body, ">икс</textarea>") {
		t.Error("the load should fill the fields")
	}

	// A corrected answer is kept, with the gotquestions id.
	form.Del("load")
	form.Set("slot1_text", "Опубликованный вопрос про ИКС.")
	form.Set("slot1_answer", "икс (поправлено)")
	form.Set("slot1_source", "книга")
	form.Set("slot1_author", "Пётр Петров")
	w.post(alice, "/nominate", form, http.StatusSeeOther)
	if n := w.count(`select count(*) from questions where gq_id = 777 and json_extract(orig, '$.answer') = 'икс (поправлено)' and tournament_title = 'Кубок'`); n != 1 {
		t.Error("the corrected question should be saved under its gotquestions id")
	}
	w.post(bob, "/nominate", slots("202", map[int]string{1: "https://gotquestions.online/question/777"}), http.StatusSeeOther)
	if w.count(`select count(*) from questions`) != 1 {
		t.Error("a second nominator of the link should share the question")
	}
}

func TestATelegramPostLoadsLikeAPastedQuestion(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	w.ts.SetTelegram(telegram.Fixed{"https://t.me/omps2026/30": {
		Text:   "Вопрос 12\nНазовите ЕГО.\n\nОтвет: стекольщик.\nИсточник: Жан Кокто. Орфей\nАвтор: Сергей Спешков\n2/42 Сборная; Коллаген",
		Photos: []string{"https://cdn.example/pic.jpg"},
	}})
	alice := w.user("alice")
	res := alice.PostJSON("/api/nominate/load", `{"link": "https://t.me/omps2026/30"}`)
	body := res.Body.String()
	if res.Code != http.StatusOK || !strings.Contains(body, `"answer":"стекольщик."`) || !strings.Contains(body, `"author":"Сергей Спешков"`) ||
		!strings.Contains(body, `"handout":"https://cdn.example/pic.jpg"`) {
		t.Errorf("load: %d %s", res.Code, body)
	}
}

func TestALinkSavedUnloadedIsLoadedOnSave(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	w.ts.SetTelegram(telegram.Fixed{"https://t.me/omps2026/30": {
		Text: "Вопрос 12\nНазовите ЕГО.\nОтвет: стекольщик.\nИсточник: Жан Кокто. Орфей\nАвтор: Сергей Спешков",
	}})
	alice := w.user("alice")
	w.post(alice, "/nominate", slots("101", map[int]string{1: "https://t.me/omps2026/30"}), http.StatusSeeOther)
	if n := w.count(`select count(*) from questions where published = 1 and link = 'https://t.me/omps2026/30' and json_extract(orig, '$.answer') = 'стекольщик.'`); n != 1 {
		t.Error("a telegram link saved as it is should be loaded into its question")
	}
	body := w.post(alice, "/nominate", slots("101", map[int]string{1: "https://example.com/post/1"}), http.StatusBadRequest)
	if !strings.Contains(body, "Не получилось загрузить вопрос") {
		t.Error("a link the form cannot read should say so")
	}
}

func TestClearEmptiesTheSlotUntilSaved(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	alice := w.user("alice")
	form := slots("101", map[int]string{1: pasted("Вопрос, который уберут.", "уберут", "Алиса")})
	w.post(alice, "/nominate", form, http.StatusSeeOther)
	form.Set("clear", "1")
	if body := w.post(alice, "/nominate", form, http.StatusOK); strings.Contains(body, "Вопрос, который уберут.") {
		t.Error("a cleared slot should come back empty")
	}
	if w.count(`select count(*) from nominations`) != 1 {
		t.Error("a clear saves nothing")
	}
}

func TestThePreviewShowsTheFields(t *testing.T) {
	w := newWorld(t)
	alice := w.user("alice")
	res := alice.PostJSON("/api/nominate/preview", `{"text": "Назовите <ИКС>.", "answer": "груша", "handout": "https://cdn.example/pic.jpg"}`)
	body := res.Body.String()
	if res.Code != http.StatusOK || !strings.Contains(body, `\u0026lt;ИКС\u0026gt;`) || !strings.Contains(body, `\u003cimg src=\"https://cdn.example/pic.jpg\"`) {
		t.Errorf("preview: %d %s", res.Code, body)
	}
}

func TestTheRulesCheckWarnsAndTheAppealReachesTheAdmins(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	w.ts.Exec(`insert into jurors(year, telegram_username, kind, name) values(2026, 'petr', 'expert', 'Пётр Петров')`)
	w.ts.SetRating(rating.Fixed{12345: {Start: "2025-09-01", End: "2025-09-02", Editors: []rating.Person{{ID: 7, Name: "Пётр Петров"}}}})
	alice := w.user("alice")
	form := slots("101", map[int]string{1: pasted("Вопрос Петра.", "петр", "Пётр Петров (Москва)")})
	w.post(alice, "/nominate", form, http.StatusSeeOther)
	page := w.get(alice, "/nominate", http.StatusOK)
	for _, want := range []string{"Отыгрыш турнира начался 1 сентября 2025", "Автор вопроса Пётр Петров входит в жюри", "Редактор турнира Пётр Петров"} {
		if !strings.Contains(page, want) {
			t.Errorf("the form should warn: %s", want)
		}
	}
	form.Set("slot1_appeal", "Это другой Пётр Петров.")
	w.post(alice, "/nominate", form, http.StatusSeeOther)
	admin := w.get(w.admin, "/admin/question/"+strconv.FormatInt(w.questionID("петр"), 10), http.StatusOK)
	if !strings.Contains(admin, "Это другой Пётр Петров.") || !strings.Contains(admin, "Автор вопроса Пётр Петров") {
		t.Error("the admin should see the check and the appeal")
	}
	res := alice.PostJSON("/api/nominate/check", `{"tournament": "https://rating.chgk.info/tournament/12345", "author": "Иван Иванов"}`)
	if !strings.Contains(res.Body.String(), "Отыгрыш турнира") || strings.Contains(res.Body.String(), "Автор вопроса") {
		t.Errorf("check: %s", res.Body.String())
	}
}
