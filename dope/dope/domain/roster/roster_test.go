package roster

import (
	"reflect"
	"testing"
)

func team(name string, number int64, players ...string) FestRosterImportTeam {
	t := FestRosterImportTeam{Name: name, Number: number, RatingID: number}
	for _, p := range players {
		t.Players = append(t.Players, FestRosterImportPlayer{FirstName: p})
	}
	return t
}

func TestSortedFestRosterImportTeamsIsAlphabeticAndLeavesInputAlone(t *testing.T) {
	in := []FestRosterImportTeam{team("Ёж", 2, "Яна", "Боря"), team("Абв", 1), team("ёж", 3)}
	out := SortedFestRosterImportTeams(in)
	names := []string{out[0].Name, out[1].Name, out[2].Name}
	if !reflect.DeepEqual(names, []string{"Абв", "Ёж", "ёж"}) {
		t.Fatalf("order %v", names)
	}
	if out[1].Players[0].FirstName != "Боря" || in[0].Players[0].FirstName != "Яна" {
		t.Fatalf("players %v / input %v", out[1].Players, in[0].Players)
	}
}

func TestSeedTeamNameKeyFoldsCaseAndYo(t *testing.T) {
	if got := SeedTeamNameKey("  Ёжики (СПб) "); got != "ежики (спб)" {
		t.Fatalf("got %q", got)
	}
}

// A Division as a scheme writes it is a Flag the Participant carries, or with
// a leading minus one it does not carry.
func TestInDivision(t *testing.T) {
	for _, c := range []struct {
		flags    []string
		division string
		want     bool
	}{
		{[]string{"Студ", "Е"}, "Студ", true},
		{[]string{"Студ"}, " Школ ", false},
		{[]string{"Студ"}, "-Студ", false},
		{[]string{"Школ"}, "- Студ", true},
		{nil, "-Студ", true},
		{nil, "Студ", false},
	} {
		if got := InDivision(c.flags, c.division); got != c.want {
			t.Errorf("InDivision(%v, %q) = %v, want %v", c.flags, c.division, got, c.want)
		}
	}
}
