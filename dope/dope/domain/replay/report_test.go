package replay

import (
	"strings"
	"testing"
)

func TestDiscrepanciesListsEveryOverrideWithItsReason(t *testing.T) {
	script, err := Parse(`[game]
type: ek
title: ЭК

[s1/r1/w1/m1] жребий
А | R---- | 10 | 1

override [s1/r1/w1/m1] место: лист сложил три темы из двенадцати
override [s1/r1/w1/m1] Σ: та же ошибка сложения
`)
	if err != nil {
		t.Fatal(err)
	}
	page := Discrepancies(script)
	for _, want := range []string{"ЭК", "s1/r1/w1/m1", "лист сложил три темы из двенадцати", "та же ошибка сложения"} {
		if !strings.Contains(page, want) {
			t.Errorf("в отчёте нет %q:\n%s", want, page)
		}
	}
}

func TestDiscrepanciesSaysSoWhenThereAreNone(t *testing.T) {
	script, err := Parse("[game]\ntype: od\ntitle: ОД\n")
	if err != nil {
		t.Fatal(err)
	}
	if page := Discrepancies(script); !strings.Contains(page, "Пока ни одного") {
		t.Errorf("пустой отчёт молчит вместо того, чтобы сказать, что всё сошлось:\n%s", page)
	}
}
