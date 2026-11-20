package tests

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	qyserver "qy/qy/server"
)

const ukText = "Тарас Шевченко каже, що ВІН завжди поруч із нами, коли ми згадуємо минуле. Назвіть ЙОГО двома словами."

// translator answers the redaction prompt with the suggestions given, and the
// translation prompt with the next of replies, counting the translations.
type translator struct {
	suggestions string
	replies     []string
	err         error
	asked       int
}

func (m *translator) answer(system, _ string) (string, error) {
	if !strings.Contains(system, "переводишь") {
		return m.suggestions, nil
	}
	m.asked++
	if m.err != nil {
		return "", m.err
	}
	r := m.replies[0]
	if len(m.replies) > 1 {
		m.replies = m.replies[1:]
	}
	return r, nil
}

// byLetters stands in for qy-langdetect: Ukrainian is whatever has an і.
func byLetters(text string) string {
	if strings.ContainsRune(text, 'і') {
		return "uk"
	}
	return "ru"
}

func translationRow(w *world, id int64) (lang, text string, edited bool, failure string) {
	w.ts.DB().QueryRow(`select coalesce(lang, ''), coalesce(json_extract(translation, '$.text'), ''), translation_edited, translation_error
from questions where id = ?`, id).Scan(&lang, &text, &edited, &failure)
	return
}

func TestAQuestionInUkrainianIsTranslatedAndTheAdminsKeepTheTranslation(t *testing.T) {
	w := newWorld(t)
	m := &translator{
		suggestions: `{"suggestions": [{"field": "text", "quote": "Тарас Шевченко", "replacement": "[автор вопроса]", "reason": "имя автора"}]}`,
		replies: []string{
			`{"text": "Тарас Шевченко говорит, что ОН всегда рядом. Назовите ЕГО.", "answer": "Палац пам’яти [прим. пер.: дворец памяти]", "comment": "Так вышло."}`,
			`{"text": "[автор вопроса] говорит, что ОН всегда рядом. Назовите ЕГО.", "answer": "Дворец памяти", "comment": "Так вышло."}`,
		},
	}
	w.ts.SetModel(&qyserver.Model{Answer: m.answer})
	w.ts.SetLangs(byLetters)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	alice := w.user("alice")
	w.post(alice, "/2026/nominate", slots("101", map[int]string{
		1: pasted(ukText, "палац пам’яті", "Тарас Шевченко"),
		2: pasted("Вчера Эдуард Голуб ел яблоко и думал об искусстве. Назовите ЕГО.", "искусство", "Эдуард Голуб"),
	}), http.StatusSeeOther)
	w.ts.ReviewPending()

	uk, ru := w.questionID("палац пам’яті"), w.questionID("искусство")
	if l, tr, _, _ := translationRow(w, uk); l != "uk" || !strings.Contains(tr, "Тарас Шевченко говорит") {
		t.Fatalf("the Ukrainian question: lang %q, translation %q", l, tr)
	}
	if l, tr, _, _ := translationRow(w, ru); l != "ru" || tr != "" {
		t.Errorf("the Russian question: lang %q, translation %q", l, tr)
	}
	if m.asked != 1 {
		t.Errorf("the model translated %d times, want once", m.asked)
	}
	href := "/admin/question/" + strconv.FormatInt(uk, 10)
	page := w.get(w.admin, href, http.StatusOK)
	if !strings.Contains(page, "Перевод сделан моделью") || !strings.Contains(page, "data-translation=") {
		t.Error("the admin should see the model's translation and the card's switch")
	}
	if strings.Contains(w.get(w.admin, "/admin/question/"+strconv.FormatInt(ru, 10), http.StatusOK), "data-translation=") {
		t.Error("a Russian question has no switch")
	}

	// Accepting a redaction changes the jury's copy, and the translation
	// follows it.
	var sid int64
	w.ts.DB().QueryRow(`select id from suggestions where question_id = ?`, uk).Scan(&sid)
	w.post(w.admin, href+"/suggestions/"+strconv.FormatInt(sid, 10), url.Values{"decision": {"accept"}}, http.StatusSeeOther)
	w.ts.ReviewPending()
	if _, tr, _, _ := translationRow(w, uk); !strings.HasPrefix(tr, "[автор вопроса] говорит") || m.asked != 2 {
		t.Fatalf("the translation should follow the redaction: %q after %d translations", tr, m.asked)
	}

	// An admin's edit stays, even once the jury's copy changes under it.
	edit := url.Values{"text": {"[автор вопроса] утверждает, что ОН всегда рядом. Назовите ЕГО."}, "answer": {"Дворец памяти"}, "comment": {"Так вышло."}}
	w.post(w.admin, href+"/translation", edit, http.StatusSeeOther)
	w.ts.Exec(`update questions set clean = json_set(clean, '$.comment', 'Иначе.') where id = ?`, uk)
	w.ts.ReviewPending()
	if _, tr, edited, _ := translationRow(w, uk); !edited || !strings.Contains(tr, "утверждает") || m.asked != 2 {
		t.Fatalf("the admin's translation was replaced: %q, edited %v, %d translations", tr, edited, m.asked)
	}
	page = w.get(w.admin, href, http.StatusOK)
	if !strings.Contains(page, "изменился после ручной правки") || !strings.Contains(page, "Иначе.") {
		t.Error("the admin should be told the jury's copy changed, and how")
	}

	// Asked to, the model translates it again.
	w.post(w.admin, href+"/retranslate", nil, http.StatusSeeOther)
	w.ts.ReviewPending()
	if _, _, edited, _ := translationRow(w, uk); edited || m.asked != 3 {
		t.Errorf("retranslate: edited %v after %d translations", edited, m.asked)
	}

	// An admin who says the question is Russian after all hides the switch.
	w.post(w.admin, href+"/lang", url.Values{"lang": {"ru"}}, http.StatusSeeOther)
	if strings.Contains(w.get(w.admin, href, http.StatusOK), "data-translation=") {
		t.Error("a question set to Russian should lose its switch")
	}
	w.post(w.admin, href+"/lang", url.Values{"lang": {"de"}}, http.StatusBadRequest)
	if res := alice.Post(href+"/translation", edit); res.Code == http.StatusSeeOther {
		t.Error("a nominator should not edit the translation")
	}
}

func TestAFailedTranslationWaitsForTheAdmin(t *testing.T) {
	w := newWorld(t)
	m := &translator{suggestions: `{"suggestions": []}`, err: errors.New("openrouter: 503")}
	w.ts.SetModel(&qyserver.Model{Answer: m.answer})
	w.ts.SetLangs(byLetters)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	w.post(w.user("alice"), "/2026/nominate", slots("101", map[int]string{1: pasted(ukText, "палац пам’яті", "Хтось")}), http.StatusSeeOther)
	w.ts.ReviewPending()
	w.ts.ReviewPending()
	uk := w.questionID("палац пам’яті")
	if _, _, _, failure := translationRow(w, uk); failure != "openrouter: 503" || m.asked != 1 {
		t.Fatalf("failure %q after %d tries", failure, m.asked)
	}
	href := "/admin/question/" + strconv.FormatInt(uk, 10)
	if !strings.Contains(w.get(w.admin, href, http.StatusOK), "Перевод не удался: openrouter: 503") {
		t.Error("the admin should see the failure")
	}
	m.err, m.replies = nil, []string{`{"text": "Назовите ЕГО.", "answer": "Дворец памяти", "comment": "Так вышло."}`}
	w.post(w.admin, href+"/retranslate", nil, http.StatusSeeOther)
	w.ts.ReviewPending()
	if _, tr, _, failure := translationRow(w, uk); tr != "Назовите ЕГО." || failure != "" {
		t.Errorf("after a retry: %q, %q", tr, failure)
	}
}
