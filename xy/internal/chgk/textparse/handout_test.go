package textparse

import (
	"strings"
	"testing"

	"xy/internal/chgk/fsource"
)

// A Word document shows the team a picture above the question text without
// anybody typing «Раздаточный материал» over it, and that picture is the
// handout (#80). chgksuite's parser does the same — the rule lives in both.

func compose4s(t *testing.T, text string) string {
	t.Helper()
	return strings.TrimSpace(fsource.Compose(Parse(text, Options{}), fsource.NumbersDefault))
}

func TestLeadingImageBecomesTheHandout(t *testing.T) {
	got := compose4s(t, "Вопрос 1.\n(img pic_001.png)\nЧто изображено?\nОтвет: круг\n")
	want := "> (img pic_001.png)\n? Что изображено?\n! круг"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestEveryLeadingImageJoinsTheHandout(t *testing.T) {
	got := compose4s(t, "Вопрос 1.\n(img a.png)\n(img b.png)\nЧто изображено?\nОтвет: круг\n")
	if !strings.HasPrefix(got, "> (img a.png)\n(img b.png)\n? Что") {
		t.Errorf("both pictures should be the handout, got:\n%s", got)
	}
}

// The guards: a picture inside the text stays where the author put it, and a
// question that is nothing but a picture keeps it — moving it would leave the
// question with no text at all.
func TestImageInsideTheQuestionStays(t *testing.T) {
	got := compose4s(t, "Вопрос 1.\nЧто изображено?\n(img pic_001.png)\nОтвет: круг\n")
	if strings.Contains(got, ">") {
		t.Errorf("a picture below the text is not the handout, got:\n%s", got)
	}
}

func TestQuestionOfNothingButAPictureKeepsIt(t *testing.T) {
	got := compose4s(t, "Вопрос 1.\n(img pic_001.png)\nОтвет: круг\n")
	if strings.Contains(got, ">") {
		t.Errorf("nothing would be left of the question, got:\n%s", got)
	}
}
