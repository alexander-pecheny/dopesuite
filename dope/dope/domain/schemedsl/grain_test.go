package schemedsl

import (
	"fmt"
	"testing"

	"dope/dope/storage/store"
)

// Where a Match sits in its Game is four coordinates — Block, Round, Wave,
// Group — and until now only the stage's code string carried them, so every
// consumer guessed by parsing `s1-g7`. These tests pin the coordinates down as
// data. A replay fixture addresses a бой by them, and a tab is built from them.

// A round-robin Block: its stages are Groups, and круги are a property of the
// бои inside one, because a Group plays all four of its круги at one стол.
func TestGrainOfRoundRobinGroups(t *testing.T) {
	src := "[defaults]\nvenues: 6\n\n[scheme]\ntype: roundrobin\ngroups: 6\nteams_in_group: 9\nmatch_size: 3\n"
	scheme := compileSrc(t, src, Input{GameType: "si"})
	stages := matchStages(scheme)
	if len(stages) != 6 {
		t.Fatalf("групп = %d, want 6", len(stages))
	}
	for i, stage := range stages {
		if stage.Grain.Block != "s1" {
			t.Errorf("группа %d: блок %q, want s1", i+1, stage.Grain.Block)
		}
		if want := fmt.Sprint(i + 1); stage.Grain.Group != want {
			t.Errorf("группа %d: group %q, want %q", i+1, stage.Grain.Group, want)
		}
		if stage.Grain.Wave != 1 {
			t.Errorf("группа %d: заход %d, want 1 — группа играет за одним столом", i+1, stage.Grain.Wave)
		}
	}
	// Nine at three seats is the affine plane: four круги of three бои.
	rounds := map[int]int{}
	for _, match := range stages[0].Matches {
		rounds[match.Round]++
	}
	if len(rounds) != 4 {
		t.Fatalf("кругов в группе = %d, want 4 (%v)", len(rounds), rounds)
	}
	for round := 1; round <= 4; round++ {
		if rounds[round] != 3 {
			t.Errorf("круг %d: %d боёв, want 3", round, rounds[round])
		}
	}
}

// An elimination Block: a Round splits into Waves when it has more бои than
// столы, and each Wave is its own stage carrying the same Round on its бои.
func TestGrainOfWaveSplitElimination(t *testing.T) {
	src := "[defaults]\nvenues: 2\n\n[scheme]\ntype: single_elimination\nteams: 8\n"
	scheme := compileSrc(t, src, Input{GameType: "brain"})
	stages := matchStages(scheme)

	type coord struct {
		block string
		wave  int
		round int
	}
	got := make([]coord, len(stages))
	for i, stage := range stages {
		round := 0
		for _, match := range stage.Matches {
			if round != 0 && match.Round != round {
				t.Fatalf("%s: бои из разных кругов в одном заходе (%d и %d)", stage.Code, round, match.Round)
			}
			round = match.Round
		}
		got[i] = coord{stage.Grain.Block, stage.Grain.Wave, round}
	}
	// Четвертьфиналы — четыре боя на двух столах, значит два захода; дальше
	// всё умещается в один.
	want := []coord{{"s1", 1, 1}, {"s1", 2, 1}, {"s1", 1, 2}, {"s1", 1, 3}}
	if len(got) != len(want) {
		t.Fatalf("этапов = %d (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("этап %d (%s) = %+v, want %+v", i+1, stages[i].Code, got[i], want[i])
		}
	}
}

// Every stage of every СтудЧР game names its Block, so nothing downstream has
// to parse a code string to find out where it sits.
func TestGrainCoversEveryStudchrStage(t *testing.T) {
	for _, c := range []struct{ name, src, gameType string }{
		{"КИнСБФ", kinsbfSrc, "brain"},
		{"ЭК", studchrEKSrc, "ek"},
		{"личная СИ", studchrSISrc, "si"},
		{"ОД", studchrODSrc, "od"},
	} {
		scheme := compileSrc(t, c.src, Input{GameType: c.gameType})
		for _, stage := range scheme.Stages {
			if stage.Grain.Block == "" {
				t.Errorf("%s: этап %q не знает своего блока", c.name, stage.Code)
			}
			if stage.StageType != "matches" {
				continue
			}
			for _, match := range stage.Matches {
				if match.Round < 1 {
					t.Errorf("%s: бой %s не знает своего круга", c.name, match.Code)
					break
				}
			}
		}
	}
}

func blocksOf(scheme store.FestScheme) []string {
	var seen []string
	for _, stage := range scheme.Stages {
		if len(seen) == 0 || seen[len(seen)-1] != stage.Grain.Block {
			seen = append(seen, stage.Grain.Block)
		}
	}
	return seen
}

// Личная СИ is two Blocks — групповой этап and плей-офф — and every stage of
// each says which it belongs to. This is what lets Сетка render two things
// instead of thirteen.
func TestGrainSeparatesBlocks(t *testing.T) {
	scheme := compileSrc(t, studchrSISrc, Input{Slug: "si", GameType: "si"})
	if got := blocksOf(scheme); len(got) != 2 || got[0] != "s1" || got[1] != "s2" {
		t.Fatalf("блоки = %v, want [s1 s2]", got)
	}
}
