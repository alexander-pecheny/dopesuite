package schemedsl

import (
	"fmt"
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
	rounds, err := planElimRounds(8, 1, fixedSize(2))
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
	rounds, err := planElimRounds(48, 2, sizeFor)
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
	if _, err := planElimRounds(10, 1, fixedSize(4)); err == nil {
		t.Error("10 into бои of 4 must not divide")
	}
	if _, err := planElimRounds(8, 2, fixedSize(2)); err == nil {
		t.Error("a бой of two cannot advance two")
	}
	if _, err := planElimRounds(8, 1, fixedSize(1)); err == nil {
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
