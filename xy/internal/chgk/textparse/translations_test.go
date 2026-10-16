package textparse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xy/internal/chgk/fsource"
)

// questionsPerStage is how many questions each stage of the championship has.
const questionsPerStage = 36

// TestTranslations reads the Open synchronous championship's packages as
// translated into other languages (testdata/translations, the text docxread
// extracts from each .docx, named season_stage.language.txt). Each must come
// out as its .canon and hold every question of its stage.
func TestTranslations(t *testing.T) {
	files, _ := filepath.Glob("testdata/translations/*.txt")
	if len(files) == 0 {
		t.Fatal("no translations")
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".txt")
		lang := name[strings.LastIndex(name, ".")+1:]
		t.Run(name, func(t *testing.T) {
			text, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			got := fsource.Compose(Parse(string(text), Options{Language: lang}), fsource.NumbersDefault)
			if n := strings.Count("\n"+got, "\n? "); n != questionsPerStage {
				t.Errorf("%d questions, want %d", n, questionsPerStage)
			}
			canon := strings.TrimSuffix(f, ".txt") + ".canon"
			if *update {
				if err := os.WriteFile(canon, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(canon)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("mismatch:\n%s", firstDiff(string(want), got))
			}
		})
	}
}
