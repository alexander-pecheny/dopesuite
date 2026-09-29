package roster

import (
	"reflect"
	"testing"
)

func pl(rating int64, name string) FestRosterImportPlayer {
	return FestRosterImportPlayer{RatingID: rating, FirstName: name}
}

func names(players []FestRosterImportPlayer) []string {
	out := []string{}
	for _, p := range players {
		out = append(out, p.FirstName)
	}
	return out
}

func desiredByName(r MergeResult) map[string][]string {
	out := map[string][]string{}
	for _, t := range r.Desired {
		out[t.Name] = names(t.Players)
	}
	return out
}

// Бобры (rating 1) and Зубры (rating 2) as the site gave them, with rows 10 and 20.
func siteRoster() []FestRosterImportTeam {
	return []FestRosterImportTeam{
		{RatingID: 1, Name: "Бобры", Players: []FestRosterImportPlayer{pl(101, "Аня"), pl(102, "Борис")}},
		{RatingID: 2, Name: "Зубры", Players: []FestRosterImportPlayer{pl(201, "Вера"), pl(202, "Глеб")}},
	}
}

func stateOf(edits ...HandEdit) HandState {
	return HandState{
		Teams: []HandTeam{
			{ID: 10, RatingID: 1, Name: "Бобры", Players: []FestRosterImportPlayer{pl(101, "Аня"), pl(102, "Борис")}},
			{ID: 20, RatingID: 2, Name: "Зубры", Players: []FestRosterImportPlayer{pl(201, "Вера"), pl(202, "Глеб")}},
		},
		Edits: edits,
	}
}

// An import of the same site roster over no edits is the site roster, each
// team on its own row.
func TestMergeHandWithoutEditsIsTheSite(t *testing.T) {
	r := MergeHand(siteRoster(), stateOf(), nil)
	if got := desiredByName(r); !reflect.DeepEqual(got, map[string][]string{"Бобры": {"Аня", "Борис"}, "Зубры": {"Вера", "Глеб"}}) {
		t.Fatalf("desired = %v", got)
	}
	if r.Desired[0].LocalID != 10 || r.Desired[1].LocalID != 20 || len(r.Conflicts) != 0 {
		t.Fatalf("rows %d, %d; conflicts %v", r.Desired[0].LocalID, r.Desired[1].LocalID, r.Conflicts)
	}
}

// The host moved Борис to Зубры and added Дина, whom the site does not know.
// The site then registers a late player, Ева, for Бобры. All three stand.
func TestMergeHandKeepsTheHostsEditsAndTakesTheSitesNews(t *testing.T) {
	site := siteRoster()
	site[0].Players = append(site[0].Players, pl(103, "Ева"))
	r := MergeHand(site, stateOf(
		HandEdit{TeamID: 10, Player: pl(102, "Борис"), Remove: true},
		HandEdit{TeamID: 20, Player: pl(102, "Борис")},
		HandEdit{TeamID: 20, Player: pl(0, "Дина")},
	), nil)
	want := map[string][]string{"Бобры": {"Аня", "Ева"}, "Зубры": {"Вера", "Глеб", "Борис", "Дина"}}
	if got := desiredByName(r); !reflect.DeepEqual(got, want) {
		t.Fatalf("desired = %v, want %v", got, want)
	}
	if len(r.Conflicts) != 0 || r.Kept.Added != 2 || r.Kept.Removed != 1 {
		t.Fatalf("conflicts %v, kept %+v", r.Conflicts, r.Kept)
	}
}

// A name, a city and Flags the host set win over the site's; a team the host
// took off stays off; a team the host made is kept with its players.
func TestMergeHandKeepsTheHostsTeams(t *testing.T) {
	name, city := "Бобры-2", "Брест"
	state := stateOf(HandEdit{TeamID: 30, Player: pl(0, "Жора")})
	state.Teams[0].HandName, state.Teams[0].HandCity = &name, &city
	state.Teams[0].HandFlags = true
	state.Teams[0].Flags = []FestRosterFlag{{Short: "Студ", Full: "Студ"}}
	state.Teams[1].HandRemoved, state.Teams[1].Deleted = true, true
	state.Teams = append(state.Teams, HandTeam{ID: 30, Hand: true, Name: "Сборная", Number: 7})
	site := siteRoster()
	site[0].Flags = nil
	r := MergeHand(site, state, nil)
	want := map[string][]string{"Бобры-2": {"Аня", "Борис"}, "Сборная": {"Жора"}}
	if got := desiredByName(r); !reflect.DeepEqual(got, want) {
		t.Fatalf("desired = %v, want %v", got, want)
	}
	if r.Desired[0].City != "Брест" || len(r.Desired[0].Flags) != 1 {
		t.Fatalf("Бобры = %+v", r.Desired[0])
	}
	if hand := r.Desired[1]; hand.LocalID != 30 || hand.Number != 7 || hand.RatingID != 0 {
		t.Fatalf("hand team = %+v", hand)
	}
	if r.Kept != (HandKept{HandTeams: 1, Renamed: 1, Flags: 1, RemovedTeams: 1, Added: 1}) {
		t.Fatalf("kept = %+v", r.Kept)
	}
}

// The host moved Борис to Зубры. The site then registers him for a new team,
// Совы. That is a conflict: the host's placement stands unless the host
// accepts the site's, and either answer is written down so it is not asked
// again.
func TestMergeHandAsksWhenTheSiteMovesAPlayerTheHostPlaced(t *testing.T) {
	state := stateOf(
		HandEdit{TeamID: 10, Player: pl(102, "Борис"), Remove: true},
		HandEdit{TeamID: 20, Player: pl(102, "Борис")},
	)
	state.Teams = append(state.Teams, HandTeam{ID: 40, RatingID: 3, Name: "Совы"})
	site := siteRoster()
	site = append(site, FestRosterImportTeam{RatingID: 3, Name: "Совы", Players: []FestRosterImportPlayer{pl(102, "Борис")}})

	r := MergeHand(site, state, nil)
	if len(r.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v", r.Conflicts)
	}
	c := r.Conflicts[0]
	if c.HandTeam != "Зубры" || c.SiteTeam != "Совы" || c.Player.FirstName != "Борис" {
		t.Fatalf("conflict = %+v", c)
	}
	if got := desiredByName(r); len(got["Совы"]) != 0 || !reflect.DeepEqual(got["Зубры"], []string{"Вера", "Глеб", "Борис"}) {
		t.Fatalf("host's placement: %v", got)
	}
	if len(r.Settle) != 1 || r.Settle[0].TeamID != 40 || !r.Settle[0].Remove {
		t.Fatalf("settle = %+v", r.Settle)
	}

	r = MergeHand(site, state, map[string]bool{c.Key: true})
	if got := desiredByName(r); !reflect.DeepEqual(got["Совы"], []string{"Борис"}) || !reflect.DeepEqual(got["Зубры"], []string{"Вера", "Глеб"}) {
		t.Fatalf("site's placement: %v", got)
	}
	if len(r.Drop) != 1 || r.Drop[0].TeamID != 20 {
		t.Fatalf("drop = %+v", r.Drop)
	}
}

// Baseline works the site's roster back from the rows and the edits, so a
// merge of it over the same edits gives the rows back.
func TestBaselineRoundTrips(t *testing.T) {
	edits := []HandEdit{
		{TeamID: 10, Player: pl(102, "Борис"), Remove: true},
		{TeamID: 20, Player: pl(102, "Борис")},
	}
	state := stateOf(edits...)
	state.Teams[0].Players = []FestRosterImportPlayer{pl(101, "Аня")}
	state.Teams[1].Players = []FestRosterImportPlayer{pl(201, "Вера"), pl(202, "Глеб"), pl(102, "Борис")}
	base := Baseline(state)
	if got := names(base[0].Players); !reflect.DeepEqual(got, []string{"Аня", "Борис"}) {
		t.Fatalf("baseline Бобры = %v", got)
	}
	r := MergeHand(base, state, nil)
	if got := desiredByName(r); !reflect.DeepEqual(got, map[string][]string{"Бобры": {"Аня"}, "Зубры": {"Вера", "Глеб", "Борис"}}) {
		t.Fatalf("round trip = %v", got)
	}
}
