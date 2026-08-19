package roster

import (
	"encoding/json"
	"reflect"
	"testing"

	"dope/dope/domain/games"
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

func TestApplyRosterToChGKStateResizesAndRemapsEntries(t *testing.T) {
	state := `{"teams":[{"name":"old"}],"entries":[[1,2,3]],"answers":[["x"]],"finished":true,"shootoutRounds":[{"teams":[1,3],"entries":[[1,3]],"answers":[]}]}`
	teams := []FestRosterImportTeam{team("A", 1), team("C", 3)}
	out, err := ApplyRosterToChGKState(state, teams, map[int]int{3: 9})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	_ = json.Unmarshal(out, &got)
	if string(got["entries"]) != `[[1,2]]` {
		t.Fatalf("entries %s", got["entries"])
	}
	if string(got["teams"]) != `[{"name":"A","number":1},{"name":"C","number":3}]` {
		t.Fatalf("teams %s", got["teams"])
	}
	if string(got["shootoutRounds"]) != `[{"teams":[1,9],"entries":[[1,9]],"answers":[]}]` {
		t.Fatalf("shootoutRounds %s", got["shootoutRounds"])
	}
	if _, ok := got["answers"]; ok {
		t.Fatal("answers kept")
	}
	if _, ok := got["finished"]; ok {
		t.Fatal("finished kept")
	}
}

func TestApplyRosterToChGKSchemeCountsTeams(t *testing.T) {
	out, err := ApplyRosterToChGKScheme(`{"nTeams":1,"extra":true}`, []FestRosterImportTeam{team("A", 1), team("B", 2)})
	if err != nil || string(out) != `{"extra":true,"nTeams":2,"teams":[{"name":"A","number":1},{"name":"B","number":2}]}` {
		t.Fatalf("got %s err %v", out, err)
	}
}

func TestRemapAnswerMatrixFollowsTeamsByNumber(t *testing.T) {
	old := []games.KSIParticipant{{Number: 1, Name: "A"}, {Number: 2, Name: "B"}, {Number: 3, Name: "B"}}
	values := [][]string{{"a1", "a2"}, {"b1"}, {"c1"}}
	// B (2) drops out, a new D joins, the other B keeps its number — and its row.
	next := []games.KSIParticipant{{Number: 3, Name: "B"}, {Number: 4, Name: "D"}, {Number: 1, Name: "A"}}
	got := RemapAnswerMatrix(values, old, next, 2)
	want := [][]string{{"c1", ""}, {"", ""}, {"a1", "a2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	// Legacy states carry names only: match by name, once each.
	legacy := []games.KSIParticipant{{Name: "A"}, {Name: "B"}}
	got = RemapAnswerMatrix([][]string{{"a"}, {"b"}}, legacy, []games.KSIParticipant{{Number: 7, Name: "B"}, {Number: 8, Name: "B"}}, 1)
	if !reflect.DeepEqual(got, [][]string{{"b"}, {""}}) {
		t.Fatalf("legacy got %v", got)
	}
	// No old participants at all: a plain positional resize.
	if got := RemapAnswerMatrix([][]string{{"a", "b", "c"}}, nil, next, 2); !reflect.DeepEqual(got, [][]string{{"a", "b"}, {"", ""}, {"", ""}}) {
		t.Fatalf("resize got %v", got)
	}
}

func TestApplyRosterToKSIStateRemapsEveryTheme(t *testing.T) {
	state := `{"participants":[{"number":1,"name":"A"},{"number":2,"name":"B"}],"themes":[{"title":"t1","answers":[["+"],["-"]]},null]}`
	teams := []FestRosterImportTeam{team("B", 2), team("A", 1)}
	out, err := ApplyRosterToKSIState(state, teams, 3)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Participants []games.KSIParticipant `json:"participants"`
		Themes       []struct {
			Title   string     `json:"title"`
			Answers [][]string `json:"answers"`
		} `json:"themes"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Themes) != 3 || got.Themes[0].Title != "t1" {
		t.Fatalf("themes %+v", got.Themes)
	}
	if !reflect.DeepEqual(got.Themes[0].Answers, [][]string{{"-", "", "", "", ""}, {"+", "", "", "", ""}}) {
		t.Fatalf("answers %v", got.Themes[0].Answers)
	}
	if len(got.Themes[2].Answers) != 2 {
		t.Fatalf("new theme rows %v", got.Themes[2].Answers)
	}
}

func TestApplyRosterToKSISchemeKeepsConfiguredThemes(t *testing.T) {
	out, err := ApplyRosterToKSIScheme(`{"themes":7}`, []FestRosterImportTeam{team("A", 1)})
	if err != nil || string(out) != `{"gameType":"ksi","participants":[{"number":1,"name":"A"}],"themes":7}` {
		t.Fatalf("got %s err %v", out, err)
	}
	out, _ = ApplyRosterToKSIScheme(``, nil)
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["themes"] != float64(games.KSIThemeCount) {
		t.Fatalf("default themes %v", got["themes"])
	}
}

func TestRawJSONObject(t *testing.T) {
	for _, raw := range []string{"", "  ", "null"} {
		if obj, err := RawJSONObject(raw); err != nil || len(obj) != 0 {
			t.Errorf("%q: %v %v", raw, obj, err)
		}
	}
	if _, err := RawJSONObject(`[1]`); err == nil {
		t.Fatal("array accepted")
	}
}

func TestSeedTeamNameKeyFoldsCaseAndYo(t *testing.T) {
	if got := SeedTeamNameKey("  Ёжики (СПб) "); got != "ежики (спб)" {
		t.Fatalf("got %q", got)
	}
}
