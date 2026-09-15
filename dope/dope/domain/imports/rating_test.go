package imports

import (
	"dope/dope/storage/store"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A rating.chgk.info result carries the team's Flags when the import asks for
// them (includeTeamFlags=1). Every one is kept, in the order the site listed
// them; a duplicate short name and a blank one are the two shapes the
// normaliser has to survive.
func TestRatingResultsCarryTeamFlags(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "rating_flags.json"))
	if err != nil {
		t.Fatal(err)
	}
	var results []ratingFestResult
	if err := json.Unmarshal(raw, &results); err != nil {
		t.Fatalf("decode rating json: %v", err)
	}
	teams, err := ratingResultsToFestRoster(results)
	if err != nil {
		t.Fatalf("normalize rating results: %v", err)
	}
	byName := map[string][]string{}
	full := map[string]map[string]string{}
	for _, team := range teams {
		for _, flag := range team.Flags {
			byName[team.Name] = append(byName[team.Name], flag.Short)
			if full[team.Name] == nil {
				full[team.Name] = map[string]string{}
			}
			full[team.Name][flag.Short] = flag.Full
		}
	}
	if got := byName["Бета"]; len(got) != 2 || got[0] != "Школ" || got[1] != "Е" {
		t.Fatalf("Beta flags = %#v, want both in source order", got)
	}
	// The duplicate is dropped; the blank short name falls back to the full one.
	if got := byName["Альфа"]; len(got) != 2 || got[0] != "Школ" || got[1] != "Студенческая команда" {
		t.Fatalf("Alpha flags = %#v, want the duplicate dropped and the blank short filled", got)
	}
	if got := full["Бета"]["Школ"]; got != "Школьная команда" {
		t.Fatalf("Beta full name = %q", got)
	}
	// A team the site flags with nothing carries no Flags at all.
	for _, team := range teams {
		if team.Name == "Гамма" && len(team.Flags) != 0 {
			t.Fatalf("unflagged team carries %#v", team.Flags)
		}
	}
}

func TestRatingResultsToFestRoster(t *testing.T) {
	raw := `[
		{
			"team":{"id":20,"name":"Beta","town":{"name":"Town B"}},
			"current":{"name":"Beta Current"},
			"position":18.5,
			"teamMembers":[{"player":{"id":200,"name":"Иван","patronymic":"Иванович","surname":"Петров"}}]
		},
		{
			"team":{"id":10,"town":{"name":"Town A"}},
			"current":{"name":"Alpha"},
			"teamMembers":[{"player":{"id":100,"name":"Анна","surname":"Сидорова"}}]
		}
	]`
	var results []ratingFestResult
	if err := json.Unmarshal([]byte(raw), &results); err != nil {
		t.Fatalf("decode rating json: %v", err)
	}

	teams, err := ratingResultsToFestRoster(results)
	if err != nil {
		t.Fatalf("normalize rating results: %v", err)
	}
	if len(teams) != 2 {
		t.Fatalf("teams = %d, want 2", len(teams))
	}
	if teams[0].Name != "Alpha" || teams[0].City != "Town A" {
		t.Fatalf("first team = %#v, want Alpha/Town A", teams[0])
	}
	if teams[1].Name != "Beta Current" {
		t.Fatalf("second team name = %q, want Beta Current", teams[1].Name)
	}
	if got := store.JoinPlayerName(teams[1].Players[0].FirstName, teams[1].Players[0].LastName); got != "Иван Петров" {
		t.Fatalf("player name = %q, want name and surname only", got)
	}
}
