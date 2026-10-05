package replay

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// Kinds is what decides how much of a transcript the HTTP twins play, so a
// field of input it does not look at would go unplayed over HTTP without
// anybody noticing. Every field of a Bout and a Seat is either input Kinds
// reads or a field it has been told is not input.
func TestKindsReadsEveryInputField(t *testing.T) {
	kinds := map[string]bool{
		"Bout.Draw": true, "Bout.Drawn": true, "Bout.Seats": true,
		"Seat.Marks": true, "Seat.Players": true, "Seat.Questions": true, "Seat.Counts": true,
		"Seat.Shootout": true, "Seat.Bet": true, "Seat.Pinned": true,
	}
	// What the sheet says it came to (asserted, not played), and where.
	notInput := map[string]bool{
		"Bout.At": true, "Bout.Line": true,
		"Seat.Name": true, "Seat.Total": true, "Seat.Place": true, "Seat.Unranked": true, "Seat.Line": true,
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[Bout](), reflect.TypeFor[Seat]()} {
		for field := range typ.Fields() {
			name := typ.Name() + "." + field.Name
			if !kinds[name] && !notInput[name] {
				t.Errorf("%s: Kinds не знает, ввод ли это; добавьте его в Bout.Kinds или в notInput", name)
			}
		}
	}
}

func TestCoveringPrefixStopsAtTheLastNewKind(t *testing.T) {
	bet := 20
	script := Script{Bouts: []Bout{
		{Draw: true, Seats: []Seat{{Marks: [][5]Mark{{Right}}}}},
		{Seats: []Seat{{Marks: [][5]Mark{{Wrong}}}}},
		{Seats: []Seat{{Marks: [][5]Mark{{Right}}}}},
		{Seats: []Seat{{Bet: &bet}}},
		{Seats: []Seat{{Marks: [][5]Mark{{Right}}}}},
	}}
	if got := script.CoveringPrefix(); got != 4 {
		t.Errorf("CoveringPrefix = %d, want 4", got)
	}
	want := []string{"bet", "finish", "marks", "seat", "seated"}
	if got := script.Kinds(); !slices.Equal(got, want) {
		t.Errorf("Kinds = %v, want %v", got, want)
	}
}

// A slice keeps only its own Matches' overrides, so an override for a Match it
// did not play is never reported as unneeded, and the whole-game assertions
// are left to the full replay.
func TestSliceKeepsItsOwnOverrides(t *testing.T) {
	first, second := Coord{Block: "s1", BlockRound: 1}, Coord{Block: "s1", BlockRound: 2}
	script := Script{
		Lineups:   []Lineup{{Team: "A"}},
		Stats:     []Stat{{Player: "x"}},
		Tables:    []Table{{At: Coord{Block: "s1"}}},
		Bouts:     []Bout{{At: first}, {At: second}},
		Overrides: []Override{{At: first}, {At: second}},
	}
	head, tail := script.Slice(0, 1), script.Slice(1, 2)
	if len(head.Overrides) != 1 || head.Overrides[0].At != first || len(head.Lineups) != 1 {
		t.Errorf("head = %+v", head)
	}
	if len(tail.Overrides) != 1 || tail.Overrides[0].At != second || tail.Lineups != nil {
		t.Errorf("tail = %+v", tail)
	}
	if head.Stats != nil || head.Tables != nil {
		t.Errorf("a slice kept the whole-game assertions: %+v", head)
	}
}

// How much of each committed transcript its HTTP twin plays, for the record:
// a prefix as long as the transcript would mean the twin saves nothing.
func TestCoveringPrefixOfTheTranscripts(t *testing.T) {
	paths, _ := filepath.Glob("../../../testdata/*/*.transcript")
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		script, err := Parse(string(src))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d of %d бои cover %v", filepath.Base(path), script.CoveringPrefix(), len(script.Bouts), script.Kinds())
	}
}
