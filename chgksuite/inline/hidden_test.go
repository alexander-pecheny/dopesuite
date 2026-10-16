package inline

import (
	"reflect"
	"strings"
	"testing"
)

// The cases chgksuite's own test suite pins for (hidden-comment …), ported so the
// two tokenizers cannot drift: a payload that reaches no export is only useful if
// both ends agree on where it starts and ends.

func plain(runs []Run) string {
	var b strings.Builder
	for _, r := range runs {
		if r.Kind == "" {
			b.WriteString(r.Text)
		}
	}
	return b.String()
}

func TestHiddenCommentDroppedByDefault(t *testing.T) {
	got := Parse4sElem("Текст (hidden-comment проверить у Ани) дальше.")
	want := []Run{{Text: "Текст дальше."}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestHiddenCommentKeptWithKeepVariant(t *testing.T) {
	got := Parse4sElemKeep("Текст (hidden-comment typst: pagebreak) дальше.")
	var found bool
	for _, r := range got {
		if r.Kind == "hidden-comment" && r.Text == "typst: pagebreak" {
			found = true
		}
	}
	if !found {
		t.Errorf("no hidden-comment run in %#v", got)
	}
	if plain(got) != "Текст дальше." {
		t.Errorf("plain text is %q", plain(got))
	}
}

func TestHiddenCommentTakesItsOwnNewline(t *testing.T) {
	got := Parse4sElem("Ответ\n(hidden-comment записка)\nещё")
	want := []Run{{Text: "Ответ\nещё"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestHiddenCommentAtFieldStartTakesFollowingWhitespace(t *testing.T) {
	for _, s := range []string{"(hidden-comment записка) Текст", "(hidden-comment записка)\nТекст"} {
		if got := plain(Parse4sElem(s)); got != "Текст" {
			t.Errorf("%q → %q, want %q", s, got, "Текст")
		}
	}
}

func TestHiddenCommentUnterminatedStaysLiteral(t *testing.T) {
	for _, s := range []string{
		"Текст (hidden-comment забыл скобку",
		"Текст (hidden-comment спросить у Ани (см. письмо)",
		"Текст (hidden-comment",
	} {
		var b strings.Builder
		for _, r := range Parse4sElem(s) {
			b.WriteString(r.Text)
		}
		if b.String() != s {
			t.Errorf("%q → %q", s, b.String())
		}
	}
}

func TestHiddenCommentSwallowsNestedDirectives(t *testing.T) {
	got := Parse4sElemKeep("Текст (hidden-comment см. (img foo.jpg)) дальше.")
	var found bool
	for _, r := range got {
		if r.Kind == "img" {
			t.Errorf("nested (img …) tokenized: %#v", got)
		}
		if r.Kind == "hidden-comment" && r.Text == "см. (img foo.jpg)" {
			found = true
		}
	}
	if !found {
		t.Errorf("no hidden-comment run in %#v", got)
	}
}

// A hidden comment is not a word boundary.
func TestHiddenCommentLeavesOneSpaceAtTheSeam(t *testing.T) {
	if got := plain(Parse4sElem("до (hidden-comment x) после")); got != "до после" {
		t.Errorf("got %q", got)
	}
}
