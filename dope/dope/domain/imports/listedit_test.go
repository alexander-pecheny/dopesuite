package imports

import (
	"reflect"
	"testing"
)

func ids(rows []ListRow) []int64 {
	out := []int64{}
	for _, r := range rows {
		out = append(out, r.TeamID)
	}
	return out
}

func rowsOf(teams ...int64) []ListRow {
	var out []ListRow
	for _, id := range teams {
		out = append(out, ListRow{TeamID: id})
	}
	return out
}

// The host took 2 out, added a one-off 9, and moved 4 to the top. The source,
// re-read, now ranks 4 3 2 1 5: the edits land on it the same way.
func TestReplayAppliesTheHostsEditsToANewList(t *testing.T) {
	var log []ListEdit
	for _, e := range []ListEdit{
		{Op: ListEditRemove, TeamID: 2},
		{Op: ListEditAdd, TeamID: 9, Name: "Гости"},
		{Op: ListEditMove, TeamID: 4, Position: 1},
	} {
		log = logListEdit(log, e)
	}
	got := Replay(rowsOf(3, 1, 2, 5, 4), log)
	if want := []int64{4, 3, 1, 5, 9}; !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("replayed = %v, want %v", ids(got), want)
	}
	if got[4].Name != "Гости" {
		t.Fatalf("the added one-off lost its name: %+v", got[4])
	}
}

// Undoing an edit forgets it: nothing is left to replay, and the entrant is
// where the source puts it.
func TestLogListEditFoldsWhatCancels(t *testing.T) {
	cases := []struct {
		name  string
		edits []ListEdit
		want  []ListEdit
	}{
		{"add then remove", []ListEdit{{Op: ListEditAdd, TeamID: 9}, {Op: ListEditRemove, TeamID: 9}}, []ListEdit{}},
		{"remove then add", []ListEdit{{Op: ListEditRemove, TeamID: 2}, {Op: ListEditAdd, TeamID: 2}}, []ListEdit{}},
		{"two moves", []ListEdit{{Op: ListEditMove, TeamID: 4, Position: 1}, {Op: ListEditMove, TeamID: 4, Position: 3}},
			[]ListEdit{{Op: ListEditMove, TeamID: 4, Position: 3}}},
		{"added, moved, removed", []ListEdit{{Op: ListEditAdd, TeamID: 9}, {Op: ListEditMove, TeamID: 9, Position: 1}, {Op: ListEditRemove, TeamID: 9}}, []ListEdit{}},
		{"moved then removed", []ListEdit{{Op: ListEditMove, TeamID: 4, Position: 1}, {Op: ListEditRemove, TeamID: 4}},
			[]ListEdit{{Op: ListEditRemove, TeamID: 4}}},
	}
	for _, c := range cases {
		log := []ListEdit{}
		for _, e := range c.edits {
			log = logListEdit(log, e)
		}
		if !reflect.DeepEqual(log, c.want) {
			t.Errorf("%s: log = %+v, want %+v", c.name, log, c.want)
		}
	}
}
