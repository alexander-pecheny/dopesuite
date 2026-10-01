package schemedsl

import "testing"

// The bronze Match's seats are the semifinal losers. Until the semifinals are
// played the Сетка shows a seat by its label, and an unlabelled one fell back
// to the internal Match code: «s2-semifinal-m12».
func TestBronzeSeatsAreLabelledByTheirBout(t *testing.T) {
	scheme := troikaPairingScheme(t, "best_of.bronze: 3")
	bronze := stageByCode(t, scheme, "s2-bronze")
	if len(bronze.Matches) == 0 {
		t.Fatal("no bronze bouts")
	}
	for _, match := range bronze.Matches {
		if len(match.Slots) != 2 {
			t.Fatalf("%s: %d seats, want 2", match.Code, len(match.Slots))
		}
		for i, want := range []string{"Бой 1, м. 2", "Бой 2, м. 2"} {
			slot := match.Slots[i]
			if slot.FromMatch == nil || slot.FromMatch.Place != 2 {
				t.Fatalf("%s seat %d = %+v, want a semifinal's 2nd place", match.Code, i+1, slot)
			}
			if slot.Label != want {
				t.Fatalf("%s seat %d label = %q, want %q", match.Code, i+1, slot.Label, want)
			}
		}
	}
}
