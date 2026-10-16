package textparse

import (
	"testing"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/i18n"
)

var creditsLines = map[string]string{
	"ru":     "Редактор благодарит за тестирование Ивана Иванова.",
	"ua":     "Редактор дякує за тестування Івану Іванову.",
	"by":     "Рэдактар дзякуе за тэставанне Івану Іванову.",
	"by_tar": "Рэдактар дзякуе за тэставаньне Івану Іванову.",
	"kz_cyr": "Редактор Иван Ивановқа алғысын білдіреді.",
	"uz_cyr": "Муҳаррир Иван Ивановга миннатдорчилик билдиради.",
	"uz":     "Muharrir Ivan Ivanovga minnatdorchilik bildiradi.",
	"en":     "The editor thanks Ivan Ivanov for testing.",
	"az":     "Redaktor İvan İvanova təşəkkür edir.",
	"sr":     "Urednik se zahvaljuje Ivanu Ivanoviću na testiranju.",
}

// TestCreditsStayOutOfFirstQuestion: in every language, the editors' thanks
// above an unmarked first question stay out of it. Azerbaijani's editor marker
// takes the line as an editor line, which is out of it too.
func TestCreditsStayOutOfFirstQuestion(t *testing.T) {
	const question = "Question text."
	for _, lang := range i18n.Languages() {
		t.Run(lang, func(t *testing.T) {
			credits, ok := creditsLines[lang]
			if !ok {
				t.Fatalf("no credits line for %s", lang)
			}
			labels, err := i18n.LabelsFor(lang, "")
			if err != nil {
				t.Fatal(err)
			}
			text := credits + "\n\n" + question + "\n\n" + labels.Field("answer") + ": 42\n"
			doc := Parse(text, Options{Language: lang})
			var meta, got string
			for _, p := range doc {
				switch p.Type {
				case "meta", "heading", "editor":
					meta = p.Content.(string)
				case "Question":
					got, _ = p.Content.(*fsource.Question).Get("question").(string)
				}
			}
			if got != question || meta != credits {
				t.Errorf("question %q, meta %q; want %q and %q", got, meta, question, credits)
			}
		})
	}
}
