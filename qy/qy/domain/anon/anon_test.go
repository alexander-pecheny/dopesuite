package anon

import (
	"reflect"
	"strings"
	"testing"

	"qy/qy/domain/question"
)

const placeholder = "[автор вопроса]"

var q = question.Question{
	Text:    "Вчера Эдуард Голуб ел яблоко и думал об искусстве. Друг Эдуарда принёс грушу.",
	Answer:  "Искусство.",
	Comment: "Подробности: https://gotquestions.online/question/123 и у Голуба в блоге.",
	Source:  "https://rating.chgk.info/tournament/5",
	Author:  "Эдуард Голуб (Москва)",
}

func TestRulesFindNamesAndLinks(t *testing.T) {
	got := Rules(q, placeholder)
	want := []Suggestion{
		{question.FieldText, "Эдуард Голуб", placeholder, ReasonAuthorName},
		{question.FieldText, "Эдуарда", placeholder, ReasonAuthorName},
		{question.FieldComment, "Голуба", placeholder, ReasonAuthorName},
		{question.FieldComment, "https://gotquestions.online/question/123", "", ReasonLink},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// A word that shares the stem is a candidate only within three letters of it.
// The admin rejects a false match such as the pigeon here; the longer word
// never reaches them.
func TestRulesStopThreeLettersPastTheStem(t *testing.T) {
	q := question.Question{Text: "Голубь сел на голубятню.", Answer: "x", Author: "Эдуард Голуб"}
	if got := Rules(q, placeholder); len(got) != 1 || got[0].Quote != "Голубь" {
		t.Errorf("got %q", got)
	}
}

func TestApply(t *testing.T) {
	got, err := Apply(q, Suggestion{Field: question.FieldText, Quote: "Эдуард Голуб", Replacement: placeholder})
	if err != nil || !strings.HasPrefix(got.Text, "Вчера [автор вопроса] ел яблоко") {
		t.Errorf("got %q, %v", got.Text, err)
	}
	if q.Text == got.Text {
		t.Error("Apply changed the original")
	}
	got, _ = Apply(q, Suggestion{Field: question.FieldComment, Quote: "https://gotquestions.online/question/123 ", Replacement: ""})
	if got.Comment != "Подробности: и у Голуба в блоге." {
		t.Errorf("got %q", got.Comment)
	}
	if _, err := Apply(q, Suggestion{Field: question.FieldText, Quote: "нет такого"}); err != ErrNotFound {
		t.Errorf("got %v", err)
	}
}

func TestPromptCarriesTheMaterial(t *testing.T) {
	system, user := Prompt(q, "Кубок Чего-то", placeholder)
	if !strings.Contains(system, "Вчера [автор вопроса] ел яблоко") || strings.Contains(system, "{{") {
		t.Error("the placeholder was not filled in")
	}
	for _, want := range []string{"Эдуард Голуб", "Кубок Чего-то", "думал об искусстве"} {
		if !strings.Contains(user, want) {
			t.Errorf("material lacks %q", want)
		}
	}
	if strings.Contains(user, "Москва") {
		t.Error("the material should carry names, not the raw credit line")
	}
}

func TestParseReplyKeepsOnlyExactQuotes(t *testing.T) {
	reply := "```json\n" + `{"suggestions": [
		{"field": "text", "quote": "Эдуард Голуб", "replacement": "[автор вопроса]", "reason": "имя"},
		{"field": "text", "quote": "чего нет в тексте", "replacement": "", "reason": "?"},
		{"field": "author", "quote": "Эдуард", "replacement": "", "reason": "?"}
	]}` + "\n```"
	got, err := ParseReply(reply, q)
	if err != nil || len(got) != 1 || got[0].Quote != "Эдуард Голуб" {
		t.Errorf("got %v, %v", got, err)
	}
	if _, err := ParseReply("no json here", q); err == nil {
		t.Error("expected an error")
	}
}
