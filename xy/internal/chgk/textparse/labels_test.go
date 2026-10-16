package textparse

import (
	"testing"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/i18n"
)

// TestReadsLabels writes a question with every field under the labels a
// language's own exports print, and again under the Russian ones a translated
// package may keep, and requires the parser to read each field back.
func TestReadsLabels(t *testing.T) {
	russian, err := i18n.LabelsFor(i18n.DefaultLanguage, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range i18n.Languages() {
		own, err := i18n.LabelsFor(lang, "")
		if err != nil {
			t.Fatal(err)
		}
		t.Run(lang+"/own", func(t *testing.T) { readsLabels(t, lang, own, own) })
		t.Run(lang+"/russian", func(t *testing.T) { readsLabels(t, lang, own, russian) })
	}
}

func readsLabels(t *testing.T, lang string, own, labels i18n.Labels) {
	fields := []string{"answer", "zachet", "nezachet", "comment", "source", "author"}
	text := questionLine(lang, own.Field("question")) + " Text of the question.\n"
	for _, f := range fields {
		text += labels.Field(f) + ": " + f + " value\n"
	}
	var q *fsource.Question
	for _, p := range Parse(text, Options{Language: lang}) {
		if got, ok := p.Content.(*fsource.Question); ok {
			q = got
		}
	}
	if q == nil {
		t.Fatalf("no question in\n%s", text)
	}
	if got := q.Get("question"); got != "Text of the question." {
		t.Errorf("question = %q", got)
	}
	for _, f := range fields {
		if got := q.Get(f); got != f+" value" {
			t.Errorf("%s (%q) = %q", f, labels.Field(f), got)
		}
	}
}

// questionLine is how a language numbers its first question.
func questionLine(lang, label string) string {
	switch lang {
	case "uz", "uz_cyr":
		return "1 – " + label + "."
	case "kz_cyr":
		return "1-" + label + "."
	}
	return label + " 1."
}
