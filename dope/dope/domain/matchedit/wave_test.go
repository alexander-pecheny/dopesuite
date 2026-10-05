package matchedit_test

import (
	"testing"

	"dope/dope/domain/matchedit"
)

// A Wave is one Block's Round at one time; a flat sitting has its stage alone.
func TestSameWave(t *testing.T) {
	bout := matchedit.Bout{StageID: 1, Block: "s2", Round: 1, Wave: 1}
	cases := []struct {
		name  string
		other matchedit.Bout
		want  bool
	}{
		{"the same wave of another stage of the Block", matchedit.Bout{StageID: 2, Block: "s2", Round: 1, Wave: 1}, true},
		{"the Round's second wave", matchedit.Bout{StageID: 1, Block: "s2", Round: 1, Wave: 2}, false},
		{"the next Round", matchedit.Bout{StageID: 1, Block: "s2", Round: 2, Wave: 1}, false},
		{"another Block", matchedit.Bout{StageID: 3, Block: "s3", Round: 1, Wave: 1}, false},
	}
	for _, c := range cases {
		if got := matchedit.SameWave(bout, c.other); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	flat := matchedit.Bout{StageID: 7}
	if !matchedit.SameWave(flat, matchedit.Bout{StageID: 7, Wave: 3}) || matchedit.SameWave(flat, matchedit.Bout{StageID: 8}) {
		t.Error("a bout with no Round goes by its stage alone")
	}
}
