package tests

import (
	"net/http"
	"strings"
	"testing"

	"qy/qy/domain/question"
	"qy/qy/platform/gq"
)

// The admins see which questions the people's jury will read: those public
// before the contest, in the question list and on the nominations page.
func TestTheListsSayWhichQuestionsArePublished(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	w.ts.SetGotQuestions(gq.Fixed{777: {Question: question.Question{
		Text: "Опубликованный вопрос про ИКС.", Answer: "икс", Source: "книга", Author: "Пётр Петров",
	}, Tournament: "Кубок", TournamentID: 12345}})
	w.post(w.user("alice"), "/2026/nominate", slots("101", map[int]string{
		1: "https://gotquestions.online/question/777",
		2: pasted("Неопубликованный вопрос.", "игрек", "Алиса"),
	}), http.StatusSeeOther)
	for _, page := range []string{"/admin/contest/2026/questions", "/admin/contest/2026/nominations"} {
		body := w.get(w.admin, page, http.StatusOK)
		if strings.Count(body, ">Опубликован<") != 1 || strings.Count(body, ">Не опубликован<") != 1 {
			t.Errorf("%s should mark one question published and one not", page)
		}
	}
}
