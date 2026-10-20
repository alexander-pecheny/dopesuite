package textparse

import (
	"testing"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/fsource"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/i18n"
)

// creditsLines are, per language, credits in the shapes real translated
// packages write them, with made-up names.
var creditsLines = map[string][]string{
	"ru":     {"Редактор благодарит за тестирование Ивана Иванова.", "Тестировали: Пётр Петров (Пермь)."},
	"ua":     {"Редактор вдячний за тестування Івану Іванову.", "Редактор дякує за допомогу Петрові Петренку."},
	"by":     {"Рэдактар тура дзякуе за тэставаньне Івану Іванову.", "Рэдактор: Пётр Пятроў (Мінск)."},
	"by_tar": {"Рэдактар тура дзякуе за тэставаньне Івану Іванову."},
	"kz_cyr": {"Тур редакторлары: Иван Иванов (Алматы).", "Редактор Иван Ивановқа алғысын білдіреді."},
	"uz_cyr": {"Муҳаррир Иван Ивановга миннатдорчилик билдиради.", "Редактор благодарит за тестирование Ивана Иванова."},
	"uz":     {"Muharrir Ivan Ivanovga minnatdorchilik bildiradi."},
	"en":     {"Edited by Ivan Ivanov.", "Tested by Petr Petrov and Anna Annova."},
	"az":     {"Redaktor İvan İvanova təşəkkür edir."},
	"sr":     {"Autori i urednici: Ivan Ivanov (Beograd).", "Urednik zahvaljuje na pomoći Petru Petroviću."},
	"ro":     {"Redactorul aduce mulțumiri pentru testare lui Ion Popescu.", "Redactori: Ion Popescu (Chișinău)."},
}

// TestCreditsStayOutOfFirstQuestion: in every language, the credits above an
// unmarked first question stay out of it.
func TestCreditsStayOutOfFirstQuestion(t *testing.T) {
	const question = "Question text."
	for _, lang := range i18n.Languages() {
		t.Run(lang, func(t *testing.T) {
			lines, ok := creditsLines[lang]
			if !ok {
				t.Fatalf("no credits lines for %s", lang)
			}
			labels, err := i18n.LabelsFor(lang, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, credits := range lines {
				text := credits + "\n\n" + question + "\n\n" + labels.Field("answer") + ": 42\n"
				var got string
				for _, p := range Parse(text, Options{Language: lang}) {
					if q, ok := p.Content.(*fsource.Question); ok {
						got, _ = q.Get("question").(string)
					}
				}
				if got != question {
					t.Errorf("%q: question %q, want %q", credits, got, question)
				}
			}
		})
	}
}
