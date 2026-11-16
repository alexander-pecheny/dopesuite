package schemedsl

import (
	"os"
	"strings"
	"testing"

	"dope/dope/domain/games"
)

// Both age groups of the Интеллектуальный воронёнок's Вопросики быстрые
// compile for an odd field: Ш ends in a final of three бои between the
// upper final's winner and IV.1's, М in one бой of nine questions.
func TestVoronenokSchemesCompile(t *testing.T) {
	for file, final := range map[string]string{"vb-sh.dsl": "s3-final", "vb-m.dsl": "s2-r5"} {
		src, err := os.ReadFile("../../../scripts/voronenok/" + file)
		if err != nil {
			t.Fatal(err)
		}
		scheme := compileSrc(t, string(src), Input{Slug: "vb", Title: "Вопросики быстрые", GameType: games.Brain, Entrants: troikaEntrants(23)})
		round7 := 0
		for _, stage := range scheme.Stages {
			if stage.Code == "s1-r7" || strings.HasPrefix(stage.Code, "s1-r7-w") {
				round7 += len(stage.Matches)
			}
		}
		if got := round7; got != 12 {
			t.Errorf("%s: round 7 has %d бои, want 11 and the empty table", file, got)
		}
		stage := stageByCode(t, scheme, final)
		wantBouts, wantQuestions := 3, 5.0
		if file == "vb-m.dsl" {
			wantBouts, wantQuestions = 1, 9
		}
		if len(stage.Matches) != wantBouts {
			t.Errorf("%s: final has %d бои, want %d", file, len(stage.Matches), wantBouts)
		}
		if q := stageConfig(t, stage)["questions"]; q != wantQuestions {
			t.Errorf("%s: final plays %v questions, want %v", file, q, wantQuestions)
		}
	}
}
