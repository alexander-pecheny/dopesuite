package tests

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"qy/qy/domain/question"
	"qy/qy/platform/gq"
	qyserver "qy/qy/server"
)

// The preliminary sheet and the questions nominated twice.

func sheetQuestions(w *world) {
	w.ts.SetGotQuestions(gq.Fixed{
		900: {Question: question.Question{Text: "Первый вопрос из предварительной таблицы.", Answer: "девятьсот"}, TournamentID: 12345},
		901: {Question: question.Question{Text: "Второй вопрос из предварительной таблицы.", Answer: "девятьсот один"}, TournamentID: 12345},
		902: {Question: question.Question{Text: "Вопрос Алисы из таблицы.", Answer: "девятьсот два"}, TournamentID: 12345},
	})
}

func row(rating int64, gqID int) qyserver.SheetRow {
	return qyserver.SheetRow{Rating: rating, At: "2026-02-02T16:36:20Z", Link: "https://gotquestions.online/question/" + strconv.Itoa(gqID)}
}

func numbered(w *world, answer string) bool {
	w.t.Helper()
	return w.count(`select count(*) from questions where json_extract(orig, '$.answer') = ? and qy is not null`, answer) == 1
}

func TestThePreliminarySheetCountsUntilTheFormIsUsed(t *testing.T) {
	w := newWorld(t)
	sheetQuestions(w)
	w.ts.SetBuff(qyserver.Buff{303: "Карина Сидорова"})
	w.ts.SetNow(day(t, "2026-11-10", 12))
	alice := w.user("alice")
	w.post(alice, "/2026/nominate", slots("101", map[int]string{1: pasted("Вопрос Алисы через форму.", "альфа", "Кто-то")}), http.StatusSeeOther)
	// Carol is only in the sheet; Alice is in it too, but used the form.
	rows := []qyserver.SheetRow{row(303, 900), row(303, 901), row(303, 900), row(101, 902)}
	if people, n := w.ts.ImportSheet(2026, rows); people != 2 || n != 3 {
		t.Fatalf("imported %d nominators and %d nominations; one question twice is one", people, n)
	}
	if people, n := w.ts.ImportSheet(2026, rows); people != 2 || n != 3 || w.count(`select count(*) from nominations where sheet = 1`) != 3 {
		t.Fatalf("a second import should replace the first: %d, %d", people, n)
	}
	body := w.get(w.admin, "/admin/contest/2026/nominations", http.StatusOK)
	for _, want := range []string{"Карина Сидорова", "Первый вопрос из предварительной таблицы.", "Из предварительной таблицы", "Не учитываются: этот игрок номинировал через форму."} {
		if !strings.Contains(body, want) {
			t.Errorf("the nominations page lacks %q", want)
		}
	}
	// The questions list and the question's page say so before numbering.
	list := w.get(w.admin, "/admin/contest/2026/questions", http.StatusOK)
	if strings.Count(list, "Не входит") != 1 || strings.Count(list, "Входит") != 3 {
		t.Errorf("the list should mark Alice's sheet question alone as out of the long list: %s", list)
	}
	alices := w.get(w.admin, "/admin/question/"+strconv.Itoa(w.count(`select id from questions where gq_id = 902`)), http.StatusOK)
	if !strings.Contains(alices, "Сейчас не входит в лонг-лист: ни одна его номинация не учитывается") {
		t.Errorf("Alice's sheet question should say why it is out")
	}
	carols := w.get(w.admin, "/admin/question/"+strconv.Itoa(w.count(`select id from questions where gq_id = 900`)), http.StatusOK)
	if !strings.Contains(carols, "Сейчас входит в лонг-лист.") {
		t.Errorf("Carol's sheet question should say it is in")
	}
	w.ts.SetNow(day(t, "2026-11-24", 12))
	w.post(w.admin, "/admin/contest/2026/deal", nil, http.StatusSeeOther)
	for answer, want := range map[string]bool{"альфа": true, "девятьсот": true, "девятьсот один": true, "девятьсот два": false} {
		if numbered(w, answer) != want {
			t.Errorf("%q numbered: %v, want %v", answer, !want, want)
		}
	}
}

func TestASheetQuestionTwoPeopleNominatedTakesNoSlot(t *testing.T) {
	w := newWorld(t)
	sheetQuestions(w)
	w.ts.Exec(`update contests set main_slots = 1 where year = 2026`)
	w.ts.SetNow(day(t, "2026-11-10", 12))
	bob := w.user("bob")
	w.post(bob, "/2026/nominate", slots("202", map[int]string{1: "https://gotquestions.online/question/900"}), http.StatusSeeOther)
	w.ts.ImportSheet(2026, []qyserver.SheetRow{row(303, 900), row(303, 901)})
	if body := w.get(w.admin, "/admin/contest/2026/nominations", http.StatusOK); !strings.Contains(body, "Вне квоты") {
		t.Error("the free question is not marked")
	}
	w.ts.SetNow(day(t, "2026-11-24", 12))
	w.post(w.admin, "/admin/contest/2026/deal", nil, http.StatusSeeOther)
	// With one slot each, Carol's 900 would have been her one question and
	// her 901 out; nominated twice, 900 takes nobody's slot.
	if !numbered(w, "девятьсот") || !numbered(w, "девятьсот один") {
		t.Error("the question nominated twice should count outside the slots")
	}
}

func TestAQuestionNominatedTwiceIsMergedAndCanBeParted(t *testing.T) {
	w := newWorld(t)
	text := "Во время сражений при Галлиполи действовал приказ не выносить раненых с поля боя. Какое слово мы заменили?"
	w.ts.SetGotQuestions(gq.Fixed{950: {Question: question.Question{Text: text, Answer: "слово"}, TournamentID: 12345}})
	w.ts.SetNow(day(t, "2026-11-10", 12))
	alice, bob := w.user("alice"), w.user("bob")
	w.post(alice, "/2026/nominate", slots("101", map[int]string{
		1: pasted(text, "слово", "Кто-то"),
		2: pasted("Совсем другой вопрос о другом, про шахматы и про Стейница с его заведением.", "шахматы", "Кто-то"),
	}), http.StatusSeeOther)
	w.post(bob, "/2026/nominate", slots("202", map[int]string{1: "https://gotquestions.online/question/950"}), http.StatusSeeOther)
	w.ts.ReviewPending()
	var pastedID, linked int64
	w.ts.DB().QueryRow(`select id from questions where gq_id is null and json_extract(orig, '$.answer') = 'слово'`).Scan(&pastedID)
	w.ts.DB().QueryRow(`select id from questions where gq_id = 950`).Scan(&linked)
	if n := w.count(`select count(*) from questions where id = ? and merged_into = ?`, pastedID, linked); n != 1 {
		t.Fatal("the pasted copy should be merged into the one with the link")
	}
	if w.count(`select count(*) from questions where merged_into is not null`) != 1 {
		t.Error("a different question was merged")
	}
	href := "/admin/question/" + strconv.FormatInt(pastedID, 10)
	if body := w.get(w.admin, href, http.StatusOK); !strings.Contains(body, "Разъединить") {
		t.Error("a merged question offers no way back")
	}
	w.post(w.admin, href+"/unmerge", url.Values{}, http.StatusSeeOther)
	if w.count(`select count(*) from questions where id = ? and merged_into is null and status = 'new'`, pastedID) != 1 {
		t.Error("unmerge should make the question stand alone again")
	}
}

func TestASheetRowMayBringItsQuestionsText(t *testing.T) {
	w := newWorld(t)
	sheetQuestions(w)
	text := pasted("Вопрос, которого нет в базе.", "небаза", "Автор Вопроса")
	rows := []qyserver.SheetRow{row(303, 900),
		{Rating: 303, At: "2026-02-03T10:00:00Z", Link: "https://rating.chgk.info/tournament/13594#40", Tournament: "https://rating.chgk.info/tournament/13594", Text: text}}
	if _, n := w.ts.ImportSheet(2026, rows); n != 2 {
		t.Fatalf("imported %d nominations, want 2", n)
	}
	if w.count(`select count(*) from questions where json_extract(orig, '$.answer') = 'небаза' and json_extract(orig, '$.author') = 'Автор Вопроса'
  and tournament_id = 13594 and published = 1 and source_text = ?`, text) != 1 {
		t.Error("the row's text should be the question, parsed")
	}
	if _, n := w.ts.ImportSheet(2026, rows); n != 2 || w.count(`select count(*) from questions`) != 2 {
		t.Error("a second import should find the question by its link")
	}
}
