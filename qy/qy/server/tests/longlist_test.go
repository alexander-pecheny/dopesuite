package tests

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// The admin sees, before numbering, which questions the nominations put in
// the long list: a reserve stays out until a main question of its nominator
// is rejected.
func TestTheAdminSeesWhetherAQuestionIsInTheLongList(t *testing.T) {
	w := newWorld(t)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	alice := w.user("alice")
	w.post(alice, "/2026/nominate", slots("101", map[int]string{
		1: pasted("Основной вопрос Алисы.", "основной", "Алиса"),
		2: pasted("Второй основной вопрос Алисы.", "второй", "Алиса"),
		3: pasted("Третий основной вопрос Алисы.", "третий", "Алиса"),
		4: pasted("Четвёртый основной вопрос Алисы.", "четвёртый", "Алиса"),
		5: pasted("Пятый основной вопрос Алисы.", "пятый", "Алиса"),
		6: pasted("Запасной вопрос Алисы.", "запасной", "Алиса"),
	}), http.StatusSeeOther)
	page := func(answer string) string {
		return w.get(w.admin, "/admin/question/"+strconv.FormatInt(w.questionID(answer), 10), http.StatusOK)
	}
	if !strings.Contains(page("основной"), "Сейчас входит в лонг-лист.") {
		t.Error("a main slot's question should be in the long list")
	}
	if !strings.Contains(page("запасной"), "Сейчас не входит в лонг-лист: он только в запасных слотах") {
		t.Error("a reserve should say it is out while the main questions stand")
	}
	if list := w.get(w.admin, "/admin/contest/2026/questions", http.StatusOK); strings.Count(list, "Не входит") != 1 {
		t.Error("the list should mark the reserve alone as out")
	}
	main := "/admin/question/" + strconv.FormatInt(w.questionID("основной"), 10)
	w.post(w.admin, main, url.Values{"status": {"rejected"}, "text": {"Основной вопрос Алисы."}, "answer": {"основной"}, "pin": {""}}, http.StatusSeeOther)
	if !strings.Contains(page("запасной"), "Сейчас входит в лонг-лист.") {
		t.Error("the reserve should stand in for the rejected question")
	}
	if strings.Contains(page("основной"), "лонг-лист") {
		t.Error("a rejected question needs no long-list note; its status says it")
	}
}
