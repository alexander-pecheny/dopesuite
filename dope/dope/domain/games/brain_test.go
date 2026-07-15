package games

import "testing"

func TestBrainMatchPoints(t *testing.T) {
	cases := []struct{ a, b, wantA, wantB int }{
		{5, 0, 2, 0},
		{2, 3, 0, 2},
		{3, 3, 1, 1},
	}
	for _, c := range cases {
		gotA, gotB := BrainMatchPoints(c.a, c.b)
		if gotA != c.wantA || gotB != c.wantB {
			t.Fatalf("BrainMatchPoints(%d,%d) = (%d,%d), want (%d,%d)", c.a, c.b, gotA, gotB, c.wantA, c.wantB)
		}
	}
}

func TestBrainGenerateScheme(t *testing.T) {
	scheme := BrainGenerateScheme("brain-1", "Брейн", 6, 4, 5)
	if scheme.GameType != BRAIN {
		t.Fatalf("gameType = %q, want %q", scheme.GameType, BRAIN)
	}
	if len(scheme.Venues) != 6 || len(scheme.Stages) != 6 {
		t.Fatalf("want 6 venues/stages, got %d/%d", len(scheme.Venues), len(scheme.Stages))
	}
	codes := map[string]bool{}
	for _, stage := range scheme.Stages {
		if len(stage.Matches) != 6 {
			t.Fatalf("stage %s: want 6 бои, got %d", stage.Code, len(stage.Matches))
		}
		for _, m := range stage.Matches {
			if codes[m.Code] {
				t.Fatalf("duplicate бой code %q", m.Code)
			}
			codes[m.Code] = true
		}
	}
}
