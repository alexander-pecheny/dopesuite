package tests

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// The admin edits the jury's copy under the nomination form's tabs: the
// handout in a field of its own, or the whole question as text.
func TestAnAdminEditsTheQuestionByItsFieldsOrAsText(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	w.post(w.user("alice"), "/2026/nominate", slots("101", map[int]string{1: pasted("Назовите ИКС.", "икс", "Алиса")}), http.StatusSeeOther)
	id := w.questionID("икс")
	href := "/admin/question/" + strconv.FormatInt(id, 10)
	clean := func() string {
		var s string
		w.ts.DB().QueryRow(`select clean from questions where id = ?`, id).Scan(&s)
		return s
	}

	page := w.get(w.admin, href, http.StatusOK)
	if !strings.Contains(page, "data-question-editor") || !strings.Contains(page, `name="raw"`) {
		t.Fatal("the page should have the editor with its three tabs")
	}
	form := url.Values{"status": {"new"}, "pin": {""}, "view": {"fields"},
		"text": {"Назовите ИКС."}, "handout": {"картинка"}, "answer": {"икс"}, "author": {"Алиса"}}
	w.post(w.admin, href, form, http.StatusSeeOther)
	if c := clean(); !strings.Contains(c, `"text":"[Раздаточный материал: картинка]\nНазовите ИКС."`) {
		t.Errorf("the handout should go in front of the text: %s", c)
	}

	form.Set("view", "text")
	form.Set("raw", "Вопрос 1. Назовите ИГРЕК.\nОтвет: игрек.\nАвтор: Алиса")
	w.post(w.admin, href, form, http.StatusSeeOther)
	if c := clean(); !strings.Contains(c, `"answer":"игрек."`) || strings.Contains(c, "картинка") {
		t.Errorf("a changed text should replace the fields: %s", c)
	}
}
