package i18n_test

import (
	"testing"

	"xy/internal/chgk/i18n"
)

func TestEveryLanguageLoads(t *testing.T) {
	for _, lang := range i18n.Languages() {
		l, err := i18n.LoadLabels(lang)
		if err != nil {
			t.Errorf("%s: %v", lang, err)
			continue
		}
		if l.Field("question") == "" || l.Field("answer") == "" {
			t.Errorf("%s: no question/answer label", lang)
		}
		if _, err := i18n.LoadRegexes(lang); err != nil {
			t.Errorf("%s: %v", lang, err)
		}
	}
}

// The markers a Russian package actually carries, through the translated
// patterns: if ForRE2 broke one, this is where it shows.
func TestRussianMarkersMatch(t *testing.T) {
	r := i18n.MustRegexes("ru")
	cases := []struct{ key, line string }{
		{"question", "Вопрос 12."},
		{"question", "Вопрос 12."},
		{"answer", "Ответ:"},
		{"zachet", "Зачёт:"},
		{"nezachet", "Незачёт:"},
		{"comment", "Комментарий:"},
		{"source", "Источник:"},
		{"author", "Автор:"},
		{"editor", "Редактор:"},
		{"handout", "Раздаточный материал:"},
		{"tour", "Тур 2"},
		{"battle", "Бой 3"},
		{"si_theme", "Тема 4. Ноль"},
	}
	for _, c := range cases {
		re := r.Get(c.key)
		if re == nil {
			t.Errorf("%s: no such pattern", c.key)
			continue
		}
		if !re.MatchString(c.line) {
			t.Errorf("%s does not match %q (pattern %s)", c.key, c.line, re)
		}
	}
	if n := r.Get("question").FindStringSubmatch("Вопрос 12."); n == nil ||
		n[r.Get("question").SubexpIndex("number")] != "12" {
		t.Errorf("the number group did not survive the NBSP: %q", n)
	}
}

func TestForRE2(t *testing.T) {
	cases := [][2]string{
		{`a\sb`, `a[\s\x{00a0}]b`},
		{`[0-9\s]*`, `[0-9\s\x{00a0}]*`},
		{`\\s`, `\\s`},
		{`[\]]\s`, `[\]][\s\x{00a0}]`},
	}
	for _, c := range cases {
		if got := i18n.ForRE2(c[0]); got != c[1] {
			t.Errorf("ForRE2(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}
