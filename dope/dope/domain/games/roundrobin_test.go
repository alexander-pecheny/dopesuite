package games

import "testing"

// every pair plays exactly once, and each бой is 2 seed slots within the group.
func TestGroupMatchesCoverEveryPairOnce(t *testing.T) {
	for size, schedule := range GroupMatches {
		seen := map[[2]int]int{}
		for _, round := range schedule {
			for _, pair := range round {
				a, b := pair[0], pair[1]
				if a < 1 || b < 1 || a > size || b > size || a == b {
					t.Fatalf("size %d: bad pair %v", size, pair)
				}
				key := [2]int{a, b}
				if a > b {
					key = [2]int{b, a}
				}
				seen[key]++
			}
		}
		want := size * (size - 1) / 2
		if len(seen) != want {
			t.Fatalf("size %d: covered %d distinct pairs, want %d", size, len(seen), want)
		}
		for pair, count := range seen {
			if count != 1 {
				t.Fatalf("size %d: pair %v played %d times, want 1", size, pair, count)
			}
		}
	}
}

func TestRoundRobinStageShape(t *testing.T) {
	// group 2 of 6, size 4: positions 1..4 take seeds (p-1)*6+2 = 2, 8, 14, 20.
	stage := RoundRobinStage(2, 4, 6, 2, 5)
	if stage.Code != "gB" || stage.StageType != "matches" {
		t.Fatalf("unexpected stage header: %+v", stage)
	}
	if len(stage.Matches) != 6 { // 4 teams → 6 бои
		t.Fatalf("want 6 бои, got %d", len(stage.Matches))
	}
	wantSeeds := map[int]bool{2: true, 8: true, 14: true, 20: true}
	for _, m := range stage.Matches {
		if m.ParticipantCount != 2 || len(m.Slots) != 2 {
			t.Fatalf("бой %s: want 2 slots, got %d", m.Code, len(m.Slots))
		}
		for _, slot := range m.Slots {
			if slot.Seed == nil || !wantSeeds[slot.Seed.Number] {
				t.Fatalf("бой %s: slot seed %+v not one of group 2's tiers", m.Code, slot.Seed)
			}
		}
		if m.Venue != 2 {
			t.Fatalf("бой %s: want venue 2, got %d", m.Code, m.Venue)
		}
	}
}

func TestCrossTableStandingsOrderingAndTies(t *testing.T) {
	// 3-team group. Team0 beats both; teams 1 and 2 tie on points but differ on +/−.
	bouts := []HeadToHead{
		{A: 0, B: 1, TakenA: 5, TakenB: 0, Finished: true}, // 0 beats 1
		{A: 0, B: 2, TakenA: 5, TakenB: 0, Finished: true}, // 0 beats 2
		{A: 1, B: 2, TakenA: 3, TakenB: 2, Finished: true}, // 1 beats 2
	}
	rows := CrossTableStandings(3, bouts, BrainMatchPoints)
	if rows[0].Team != 0 || rows[0].Points != 4 || rows[0].Place != 1 {
		t.Fatalf("place 1 wrong: %+v", rows[0])
	}
	if rows[1].Team != 1 || rows[1].Points != 2 || rows[1].Place != 2 {
		t.Fatalf("place 2 wrong: %+v", rows[1])
	}
	if rows[2].Team != 2 || rows[2].Place != 3 {
		t.Fatalf("place 3 wrong: %+v", rows[2])
	}
	// team1: took 3, conceded 5+2=7 → diff -4; team2: took 0+2=2, conceded 5+3=8 → diff -6
	if rows[1].Diff <= rows[2].Diff {
		t.Fatalf("expected team1 diff > team2 diff, got %d vs %d", rows[1].Diff, rows[2].Diff)
	}
}

func TestCrossTableStandingsSharedPlaceOnFullTie(t *testing.T) {
	// Two teams, бой not finished → no points, equal everything → shared place 1.
	bouts := []HeadToHead{{A: 0, B: 1, TakenA: 2, TakenB: 2, Finished: false}}
	rows := CrossTableStandings(2, bouts, BrainMatchPoints)
	if rows[0].Place != 1 || rows[1].Place != 1 {
		t.Fatalf("full tie should share place 1, got %d and %d", rows[0].Place, rows[1].Place)
	}
}
