package tests

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// An admin works through the questions with suggestions waiting, one after
// another, without going back to the list.
func TestTheQuestionsWithSuggestionsWaitingAreWalkedThrough(t *testing.T) {
	w := newWorld(t)
	for _, id := range []string{"1", "2", "3"} {
		w.ts.Exec(`insert into questions(id, year, source_text, ai_status, created_at, updated_at) values(` + id + `, 2026, 'x', 'done', '2026-11-10T00:00:00Z', '2026-11-10T00:00:00Z')`)
	}
	for _, id := range []string{"1", "3"} {
		w.ts.Exec(`insert into suggestions(question_id, field, quote, replacement, source, created_at) values(` + id + `, 'comment', 'а', 'б', 'ai', '2026-11-10T00:00:00Z')`)
	}
	list := w.get(w.admin, "/admin/contest/2026/questions?in=pending", http.StatusOK)
	if !strings.Contains(list, `/admin/question/1?in=pending"`) || !strings.Contains(list, `/admin/question/3?in=pending"`) || strings.Contains(list, `/admin/question/2?`) {
		t.Error("the filter should keep the questions with suggestions waiting, and their links the filter")
	}
	first := w.get(w.admin, "/admin/question/1?in=pending", http.StatusOK)
	if !strings.Contains(first, `href="/admin/question/3?in=pending"`) || !strings.Contains(first, "1 из 2") {
		t.Error("the first question should lead on to the next one with suggestions, and say where it stands")
	}
	res := w.admin.Post("/admin/question/1/suggestions/1?in=pending", url.Values{"decision": {"reject"}})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/question/1?in=pending" {
		t.Fatalf("deciding a suggestion should stay in the filter: %d %s", res.Code, res.Header().Get("Location"))
	}
	// Its last suggestion decided, the question has left the filter, and
	// still leads on.
	first = w.get(w.admin, "/admin/question/1?in=pending", http.StatusOK)
	if !strings.Contains(first, `href="/admin/question/3?in=pending"`) || strings.Contains(first, " из ") {
		t.Error("a question the filter no longer keeps should still lead to the next one")
	}
	middle := w.get(w.admin, "/admin/question/2", http.StatusOK)
	if !strings.Contains(middle, `href="/admin/question/1"`) || !strings.Contains(middle, `href="/admin/question/3"`) || !strings.Contains(middle, "2 из 3") {
		t.Error("without a filter a question leads to its neighbours in the whole list")
	}
}
