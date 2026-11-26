package games

import "testing"

func TestQuestionRunsNamesRunsByTheirEnds(t *testing.T) {
	if got := questionRuns([]int{20, 21, 22, 23, 30, 31, 40}); got != "21–24, 31, 32, 41" {
		t.Fatalf("questionRuns = %q", got)
	}
}
