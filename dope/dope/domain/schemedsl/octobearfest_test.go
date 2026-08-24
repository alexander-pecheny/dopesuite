package schemedsl

import (
	"os"
	"testing"
)

// Троечка's регламент §5.1–5.5 as a scheme: 48 команд, восемь групп по шесть,
// потом четыре по четыре, потом две по четыре, а финал и матч за 3-е место —
// по три боя. The replay plays this very file (server/tests), so what it
// expands to is worth pinning here where a failure names the shape.
func TestTroikaOctobearfestScheme(t *testing.T) {
	src, err := os.ReadFile("../../../scripts/troika/troika.dsl")
	if err != nil {
		t.Fatal(err)
	}
	scheme := compileSrc(t, string(src), troikaInput(48))
	for _, want := range []struct {
		code    string
		kind    string
		matches int
	}{
		{"s1-g1", "rr", 15}, {"s1-g8", "rr", 15},
		{"s2-g1", "rr", 6}, {"s2-g4", "rr", 6},
		{"s3-g1", "rr", 6}, {"s3-g2", "rr", 6},
		// Оба финала — серии: их решает сумма рейтинговых баллов.
		{"s4-final", "series", 3}, {"s4-bronze", "series", 3},
	} {
		stage := stageByCode(t, scheme, want.code)
		if stage.Kind != want.kind || len(stage.Matches) != want.matches {
			t.Errorf("%s: kind %s, боёв %d; жду %s и %d",
				want.code, stage.Kind, len(stage.Matches), want.kind, want.matches)
		}
	}
}
