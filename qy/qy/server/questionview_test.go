package qyserver

import (
	"encoding/json"
	"html"
	"regexp"
	"strings"
	"testing"

	"qy/qy/domain/question"
)

// The card sends its fields to the browser's renderer, the handout split off
// the text, and keeps the qy-only lines (the tournament) in its disclosure.
func TestTheCardSendsItsFieldsToTheRenderer(t *testing.T) {
	out := questionHTML(card{qy: 12, tournament: "Кубок", q: question.Question{
		Text: "[Раздаточный материал: яблоко]\nНазовите ИКС.", Answer: "цирк", Author: "!!Авторка Юлия"}})
	m := regexp.MustCompile(`data-question="([^"]*)" data-number="QY12"`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no data-question in %s", out)
	}
	var f map[string]string
	if err := json.Unmarshal([]byte(html.UnescapeString(m[1])), &f); err != nil {
		t.Fatal(err)
	}
	if f["handout"] != "яблоко" || f["text"] != "Назовите ИКС." || f["author"] != "!!Авторка Юлия" {
		t.Errorf("fields = %v", f)
	}
	if !regexp.MustCompile(`id="QY12"`).MatchString(out) || !regexp.MustCompile(`pv-label">Турнир: </strong>Кубок`).MatchString(out) {
		t.Errorf("no anchor or tournament in %s", out)
	}
}

// A translated card carries both versions for the switch, which waits hidden
// for the script, and the translation as plain text for a page without it.
func TestATranslatedCardCarriesBothVersions(t *testing.T) {
	tr := question.Question{Text: "Назовите ЕГО.", Answer: "дворец памяти"}
	out := questionHTML(card{qy: 3, q: question.Question{Text: "Назвіть ЙОГО.", Answer: "палац пам’яті"}, tr: &tr})
	for _, want := range []string{`data-translation="`, `data-part="langs" hidden`, `data-view="orig"`, `data-view="tr"`,
		`<details data-part="translation">`, "Назовите ЕГО."} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in %s", want, out)
		}
	}
	if strings.Contains(questionHTML(card{qy: 3, q: tr}), "data-translation") {
		t.Error("a card without a translation has no switch")
	}
}
