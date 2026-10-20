package inline

import (
	"reflect"
	"testing"
)

func TestSplitHandoutsCutsAtAHandoutOnItsOwnLines(t *testing.T) {
	got := SplitHandouts("[Ведущему: медленно]\n[Раздаточный материал:\nстрока\nещё строка\n]\nЧто это?")
	want := []Piece{
		{Text: "[Ведущему: медленно]"},
		{Handout: true, Label: "Раздаточный материал", Text: "строка\nещё строка"},
		{Text: "Что это?"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}

func TestSplitHandoutsLeavesAHandoutInsideASentenceAlone(t *testing.T) {
	for _, s := range []string{
		"Взгляните на [Раздаточный материал: АБВ] и ответьте.",
		"[Раздаточный материал]\nЧто это?",
		"[Ведущему: не раздавать]\nЧто это?",
	} {
		if got := SplitHandouts(s); !reflect.DeepEqual(got, []Piece{{Text: s}}) {
			t.Errorf("%q: got %#v", s, got)
		}
	}
}

func TestSplitHandoutsTakesAQuestionThatIsOnlyAHandout(t *testing.T) {
	got := SplitHandouts("[Раздаточный материал: (img a.png)]")
	want := []Piece{{Handout: true, Label: "Раздаточный материал", Text: "(img a.png)"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}
