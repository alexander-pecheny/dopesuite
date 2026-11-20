package translate

import (
	"strings"
	"testing"

	"qy/qy/domain/question"
)

var q = question.Question{
	Text:    "[автор вопроса] каже, що ВІН завжди поруч. Назвіть ЙОГО.",
	Answer:  "Палац пам’яті",
	Comment: "Мнемотехніка.",
	Source:  "https://uk.wikipedia.org/wiki/Палац_пам'яті",
}

func TestPromptNamesTheLanguageAndCarriesTheFields(t *testing.T) {
	system, user := Prompt(q, "украинском")
	if !strings.Contains(system, "на украинском") || strings.Contains(system, "{{") {
		t.Errorf("system = %q", system)
	}
	if !strings.Contains(user, "Назвіть ЙОГО") || strings.Contains(user, "wikipedia") || strings.Contains(user, "zachet") {
		t.Errorf("user = %q", user)
	}
}

func TestParseReplyTranslatesAndKeepsTheRest(t *testing.T) {
	got, err := ParseReply("```json\n"+`{"text": "[автор вопроса] говорит, что ОН всегда рядом. Назовите ЕГО.",
		"answer": "Дворец памяти", "comment": "Мнемотехника.", "zachet": "выдумка"}`+"\n```", q)
	if err != nil {
		t.Fatal(err)
	}
	want := question.Question{
		Text: "[автор вопроса] говорит, что ОН всегда рядом. Назовите ЕГО.", Answer: "Дворец памяти",
		Comment: "Мнемотехника.", Source: q.Source,
	}
	if got != want {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestParseReplyRefusesAGap(t *testing.T) {
	if _, err := ParseReply(`{"text": "Назовите ЕГО."}`, q); err == nil {
		t.Error("a reply without the answer should fail")
	}
	if _, err := ParseReply("нет", q); err == nil {
		t.Error("a reply without JSON should fail")
	}
}
