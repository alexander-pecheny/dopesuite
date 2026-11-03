package eligibility

import "testing"

var window = Window{From: "2025-11-01", To: "2026-10-31", Shortlist: "2027-01-10"}

func kinds(ps []Problem) []Kind {
	var out []Kind
	for _, p := range ps {
		out = append(out, p.Kind)
	}
	return out
}

func TestAQuestionInsideTheRulesPasses(t *testing.T) {
	got := Check(window, Tournament{Start: "2026-05-17", End: "2026-05-18"}, "Иван Иванов (Москва)", []Person{{Name: "Пётр Петров"}})
	if len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

func TestTheDatesAreChecked(t *testing.T) {
	got := kinds(Check(window, Tournament{Start: "2025-10-20", End: "2027-02-01"}, "", nil))
	if len(got) != 2 || got[0] != DatesOff || got[1] != StillPlayable {
		t.Errorf("got %v", got)
	}
	if got := Check(window, Tournament{}, "", nil); len(got) != 0 {
		t.Errorf("an unknown tournament is not checked: %v", got)
	}
}

func TestAJurorWhoWroteOrEditedIsFound(t *testing.T) {
	barred := []Person{{Name: "Пётр Петров"}, {Name: "Анна Смирнова", RatingID: 42}, {Name: "Вася"}}
	got := Check(window, Tournament{Start: "2026-05-17", Editors: []Person{{Name: "Анна Смирнова-Кох", RatingID: 42}, {Name: "Вася Пупкин"}}},
		"Иван Иванов, Петр Петров (Санкт-Петербург)", barred)
	if len(got) != 2 || got[0].Kind != AuthorBarred || got[0].Who != "Пётр Петров" || got[1].Kind != EditorBarred {
		t.Errorf("got %v", got)
	}
	if got := Check(window, Tournament{}, "Пётр Иванов и Анна Петрова", barred); len(got) != 0 {
		t.Errorf("parts of two names are not one name: %v", got)
	}
}
