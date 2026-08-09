package schemedsl

import (
	"fmt"
	"strings"
	"testing"
)

func fixedSize(size int) func(int, int) int {
	return func(int, int) int { return size }
}

func shape(rounds []elimRound) string {
	out := ""
	for _, r := range rounds {
		out += fmt.Sprintf("%dx%d ", r.bouts, r.size)
	}
	return out
}

// The classic halving bracket is the general engine at its smallest settings.
func TestPlanClassicBracket(t *testing.T) {
	rounds, err := planElimRounds(8, 1, 0, fixedSize(2))
	if err != nil {
		t.Fatalf("planElimRounds: %v", err)
	}
	if got := shape(rounds); got != "4x2 2x2 1x2 " {
		t.Fatalf("shape = %q", got)
	}
	if !rounds[len(rounds)-1].terminal {
		t.Error("the final must be the terminal бой")
	}
	if rounds[0].survivors(1) != 4 {
		t.Errorf("survivors = %d, want 4", rounds[0].survivors(1))
	}
}

// ЭК: 48 играющих, бои по четыре, проходят двое — и четвертьфинал, который
// регламент играет втроём.
func TestPlanEKBracket(t *testing.T) {
	sizeFor := func(round, entering int) int {
		if entering == 12 {
			return 3
		}
		return 4
	}
	rounds, err := planElimRounds(48, 2, 0, sizeFor)
	if err != nil {
		t.Fatalf("planElimRounds: %v", err)
	}
	if got := shape(rounds); got != "12x4 6x4 4x3 2x4 1x4 " {
		t.Fatalf("shape = %q, want 12x4 6x4 4x3 2x4 1x4", got)
	}
	if !rounds[4].terminal || rounds[4].entering != 4 {
		t.Errorf("final = %+v, want a terminal бой of 4", rounds[4])
	}
}

func TestPlanRejectsImpossibleShapes(t *testing.T) {
	if _, err := planElimRounds(10, 1, 0, fixedSize(4)); err == nil {
		t.Error("10 into бои of 4 must not divide")
	}
	if _, err := planElimRounds(8, 2, 0, fixedSize(2)); err == nil {
		t.Error("a бой of two cannot advance two")
	}
	if _, err := planElimRounds(8, 1, 0, fixedSize(1)); err == nil {
		t.Error("a бой of one is not a бой")
	}
}

// The first бой takes the best and the worst — СтудЧР's ПО-1 exactly.
func TestSnakeChunksMatchTheStudchrSheets(t *testing.T) {
	got := snakeChunks(24, 6)
	want := [][]int{
		{1, 12, 13, 24}, {2, 11, 14, 23}, {3, 10, 15, 22},
		{4, 9, 16, 21}, {5, 8, 17, 20}, {6, 7, 18, 19},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ПО-1 = %v\nwant     %v", got, want)
	}
	if got := snakeChunks(12, 3); fmt.Sprint(got) != fmt.Sprint([][]int{{1, 6, 7, 12}, {2, 5, 8, 11}, {3, 4, 9, 10}}) {
		t.Fatalf("ПО-2 верхняя = %v", got)
	}
	if got := snakeChunks(6, 2); fmt.Sprint(got) != fmt.Sprint([][]int{{1, 4, 5}, {2, 3, 6}}) {
		t.Fatalf("ПО-3 верхняя = %v", got)
	}
	if got := snakeChunks(8, 2); fmt.Sprint(got) != fmt.Sprint([][]int{{1, 4, 5, 8}, {2, 3, 6, 7}}) {
		t.Fatalf("ПО-4 нижняя = %v", got)
	}
}

func ranksOf(plan *dePlan, round int) [][]int {
	var out [][]int
	for _, index := range plan.rounds[round] {
		var ranks []int
		for _, source := range plan.bouts[index].sources {
			ranks = append(ranks, source.rank)
		}
		out = append(out, ranks)
	}
	return out
}

// КИнСБФ's pod of four is a double elimination at two seats a бой: five бои,
// and it stops the moment its two qualifiers exist — no grand final, because
// the pod's job was to qualify two, not to crown anyone.
func TestPlanKinsbfPod(t *testing.T) {
	plan, err := planLives(4, 2, 1, 2, fixedSize(2))
	if err != nil {
		t.Fatalf("planLives: %v", err)
	}
	if len(plan.bouts) != 5 {
		t.Fatalf("боёв = %d, want 5", len(plan.bouts))
	}
	if got := fmt.Sprint(plan.rounds); got != "[[0 1] [2 3] [4]]" {
		t.Fatalf("rounds = %s, want [[0 1] [2 3] [4]]", got)
	}
	// Бой 3 is the winners' бой, бой 4 the losers', бой 5 the cross: the бой-3
	// loser against the бой-4 winner.
	want := []string{
		"1,4", "2,3", // openers, snake-drawn
		"b0#1,b1#1", "b0#2,b1#2",
		"b2#2,b3#1",
	}
	for i, bout := range plan.bouts {
		if got := describeBout(bout); got != want[i] {
			t.Errorf("бой %d = %s, want %s", i+1, got, want[i])
		}
	}
	if len(plan.survivor) != 2 {
		t.Fatalf("survivors = %d, want 2", len(plan.survivor))
	}
}

// The whole личная СИ play-off, against the reference sheets' PO_1..PO_5.
// Every бой of every round is a rank tuple in generate_si.py; if the model is
// right, these fall out of «24 игрока, бой на четверых, проходят двое, две
// жизни» and nothing else.
func TestPlanStudchrSIPlayoff(t *testing.T) {
	plan, err := planLives(24, 2, 2, 1, fixedSize(4))
	if err != nil {
		t.Fatalf("planLives: %v", err)
	}
	want := [][][]int{
		{{1, 12, 13, 24}, {2, 11, 14, 23}, {3, 10, 15, 22}, {4, 9, 16, 21}, {5, 8, 17, 20}, {6, 7, 18, 19}},
		{{1, 6, 7, 12}, {2, 5, 8, 11}, {3, 4, 9, 10}, {13, 18, 19, 24}, {14, 17, 20, 23}, {15, 16, 21, 22}},
		{{1, 4, 5}, {2, 3, 6}, {7, 12, 13, 18}, {8, 11, 14, 17}, {9, 10, 15, 16}},
		{{1, 2, 3, 4}, {5, 8, 9, 12}, {6, 7, 10, 11}},
		{{3, 6, 7}, {4, 5, 8}},
		{{3, 4, 5, 6}}, // финал нижней сетки
		{{1, 2, 3, 4}}, // гранд-финал
	}
	if len(plan.rounds) != len(want) {
		t.Fatalf("раундов = %d, want %d", len(plan.rounds), len(want))
	}
	names := []string{"ПО-1", "ПО-2", "ПО-3", "ПО-4", "ПО-5", "финал н/с", "грандфинал"}
	for r := range want {
		if got := fmt.Sprint(ranksOf(plan, r)); got != fmt.Sprint(want[r]) {
			t.Errorf("%s = %v\n%*swant %v", names[r], ranksOf(plan, r), len(names[r]), "", want[r])
		}
	}
	if len(plan.survivor) != 4 {
		t.Fatalf("грандфинал должен раздать 4 места, got %d", len(plan.survivor))
	}
}

func describeBout(bout deBout) string {
	parts := make([]string, len(bout.sources))
	for i, source := range bout.sources {
		if source.entrant != 0 {
			parts[i] = fmt.Sprint(source.entrant)
		} else {
			parts[i] = fmt.Sprintf("b%d#%d", source.bout, source.place)
		}
	}
	return strings.Join(parts, ",")
}
