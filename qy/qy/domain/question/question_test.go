package question

import (
	"reflect"
	"testing"
)

const pasted = `Название турнира: Кубок Чего-то
Ссылка на турнир: https://rating.chgk.info/tournament/12345
https://gotquestions.online/question/777
Вопрос 7.
Вчера Эдуард Голуб ел яблоко и думал об ИКСЕ. Назовите ИКС.
Ответ: искусство.
Зачёт: арт.
Комментарий: яблоко — это искусство.
Источник:
1. https://example.com
2. Книга.
Автор: Эдуард Голуб (Москва), Анна Иванова`

func TestParseDropsWhereTheQuestionCameFrom(t *testing.T) {
	q, ok := Parse(pasted)
	if !ok {
		t.Fatal("no answer found")
	}
	want := Question{
		Text:    "Вчера Эдуард Голуб ел яблоко и думал об ИКСЕ. Назовите ИКС.",
		Answer:  "искусство.",
		Zachet:  "арт.",
		Comment: "яблоко — это искусство.",
		Source:  "1. https://example.com\n2. Книга.",
		Author:  "Эдуард Голуб (Москва), Анна Иванова",
	}
	if q != want {
		t.Errorf("got  %+v\nwant %+v", q, want)
	}
}

func TestParseWithoutAnAnswerIsNotOK(t *testing.T) {
	if _, ok := Parse("Просто текст без ответа."); ok {
		t.Error("ok without an answer")
	}
	if _, ok := Parse("  \n"); ok {
		t.Error("ok on blank input")
	}
}

func TestFindLinks(t *testing.T) {
	l := FindLinks(pasted)
	if l.GotQuestionsID != 777 || l.TournamentID != 12345 || l.GotQuestions != "https://gotquestions.online/question/777" {
		t.Errorf("got %+v", l)
	}
}

func TestAuthorNames(t *testing.T) {
	got := AuthorNames("Эдуард Голуб (Москва), Анна Иванова и Пётр Петров")
	want := []string{"Эдуард Голуб", "Анна Иванова", "Пётр Петров"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
}

func TestNormalAnswer(t *testing.T) {
	if NormalAnswer("Ёлка!") != NormalAnswer("елка") {
		t.Error("ё and punctuation should not tell two answers apart")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	q, _ := Parse(pasted)
	if FromJSON(q.JSON()) != q {
		t.Error("round trip changed the question")
	}
	if q.Anonymous().Author != "" {
		t.Error("anonymous copy kept the author")
	}
}

func TestSplitHandoutUndoesWithHandout(t *testing.T) {
	handout, rest := SplitHandout(WithHandout("Назовите ИКС.", "Картинка с яблоком"))
	if handout != "Картинка с яблоком" || rest != "Назовите ИКС." {
		t.Fatalf("got %q, %q", handout, rest)
	}
	if h, r := SplitHandout("Назовите ИКС."); h != "" || r != "Назовите ИКС." {
		t.Fatalf("a text without a handout changed: %q, %q", h, r)
	}
}

func TestParseKeepsTheHandoutInTheText(t *testing.T) {
	q, ok := Parse("Вопрос 1. [Раздаточный материал: яблоко]\nНазовите ИКС.\nОтвет: груша.\nИсточник: книга.\nАвтор: Анна Иванова")
	if !ok {
		t.Fatal("no answer found")
	}
	if h, rest := SplitHandout(q.Text); h != "яблоко" || rest != "Назовите ИКС." {
		t.Fatalf("got %q / %q from %q", h, rest, q.Text)
	}
}
