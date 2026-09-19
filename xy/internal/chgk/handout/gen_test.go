package handout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xy/internal/chgk/fsource"
)

// A parsed .docx usually carries the handout without the bracket: the label on
// a line of its own, then the picture or the text. Those are still handouts.
func TestGenerateUnbracketed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "? Раздаточный материал.\n(img pic.png)\nЧто изображено?\n! А\n\n" +
		"? Раздаточный материал\nThere is ******* of ******.\nВосстановите слова.\n! Б\n\n" +
		"? Без картинки.\n! В\n"
	files, warnings, err := Generate(fsource.Parse(src, "chgk"), "pack", dir, GenerateOptions{Language: "ru"})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 2 {
		t.Errorf("want a warning per unbracketed handout, got %v", warnings)
	}
	want := "for_question: 1\ncolumns: 3\n\nimage: " + filepath.Join(dir, "pic.png") +
		"\n---\nfor_question: 2\ncolumns: 3\n\nThere is ******* of ******.\nВосстановите слова."
	if len(files) != 1 || files[0].Content != want {
		t.Errorf("got:\n%s\nwant:\n%s", files[0].Content, want)
	}
}

// A blank line inside the handout text is an empty line on the handout; the
// one under the settings and any around the text are not.
func TestParseHandoutsKeepsBlankLinesInText(t *testing.T) {
	blocks := parseHandouts("for_question: 1\ncolumns: 3\n\nfirst\n\nsecond\n\n")
	if got := blocks[0]["text"]; got != "first\\\n\\\nsecond" {
		t.Errorf("text = %q", got)
	}
	if !strings.Contains(GenerateTyp("columns: 3\n\nfirst\n\nsecond", DefaultArgs()), "[first\\\n\\\nsecond]") {
		t.Error("the empty line did not reach the .typ")
	}
}
