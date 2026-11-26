package games

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// What FormatMultiGames writes parses back to the same minigames, so the
// settings page can show a Game's spec and save it untouched.
func TestFormatMultiGamesRoundTrips(t *testing.T) {
	for _, src := range []string{
		"Морской бой →0..100: Ячейки{0-20} Корабли{0-10}\nКатакана →0..100: {0-3}x20",
		"Песни: {0,1,2}x3 | {0,1}x10 {-1,0,1}",
		"Медиа-Эрудит (вне итога) →0..100: {0-1000}\nШтраф: {-3--1} {0,5,10}",
	} {
		want, err := ParseMultiGames(src)
		if err != nil {
			t.Fatal(err)
		}
		text := FormatMultiGames(want)
		got, err := ParseMultiGames(text)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q came back as %q", src, text)
		}
	}
	games, _ := ParseMultiGames("Катакана →0..100: {0-3}x20")
	if text := FormatMultiGames(games); text != "Катакана ->0..100: {0-3}x20" {
		t.Errorf("Катакана = %q", text)
	}
}

func multiShapeFixture(t *testing.T, spec string, cells string) (string, string) {
	t.Helper()
	minigames, err := ParseMultiGames(spec)
	if err != nil {
		t.Fatal(err)
	}
	scheme, _ := json.Marshal(map[string]any{"gameType": Multi, "title": "Медийки", "minigames": minigames, "sorting": []string{"total"}})
	return string(scheme), `{"participants":[{"number":1,"name":"А"},{"number":2,"name":"Б"}],"finished":false,"games":` + cells + `}`
}

// The Воронёнок's media games, reshaped after Морской бой was entered: the
// Медиа-Эрудит goes, Катакана splits into its matrices, and Морской бой's
// cells stay where they were, found by its name.
func TestReshapeKeepsAMinigamesCellsUnderItsName(t *testing.T) {
	scheme, state := multiShapeFixture(t, "Морской бой →0..100: Ячейки{0-20} Корабли{0-10}\nМедиа-Эрудит (вне итога): {0-1000}\nКатакана →0..100: {0-100}",
		`[{"cells":[[12,4],[20,10]]},{"cells":[]},{"cells":[[0],[0]]}]`)
	newScheme, newState, err := (multi{}).Reshape(scheme, state, "Катакана →0..100: {0-3}x20\nМорской бой →0..100: Ячейки{0-20} Корабли{0-10}", "total")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Participants []KSIParticipant `json:"participants"`
		Games        []struct {
			Cells [][]int `json:"cells"`
		} `json:"games"`
		Shape string `json:"shape"`
	}
	if err := json.Unmarshal(newState, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Participants) != 2 || len(doc.Games) != 2 || doc.Shape == "" {
		t.Fatalf("state = %s", newState)
	}
	if !reflect.DeepEqual(doc.Games[1].Cells, [][]int{{12, 4}, {20, 10}}) {
		t.Errorf("Морской бой = %v", doc.Games[1].Cells)
	}
	if len(doc.Games[0].Cells) != 2 || len(doc.Games[0].Cells[0]) != 20 {
		t.Errorf("Катакана = %v", doc.Games[0].Cells)
	}
	spec, sorting := (multi{}).Shape(string(newScheme))
	if spec != "Катакана ->0..100: {0-3}x20\nМорской бой ->0..100: Ячейки{0-20} Корабли{0-10}" || sorting != "total" {
		t.Errorf("shape = %q, %q", spec, sorting)
	}
	if !strings.Contains(string(newScheme), `"title":"Медийки"`) {
		t.Errorf("the scheme lost its title: %s", newScheme)
	}
}

// Nothing entered is lost: a reshape that drops a minigame or a task holding
// a value, or that no longer takes a value entered, is refused. A renamed
// minigame is a new one.
func TestReshapeRefusesToLoseAValue(t *testing.T) {
	scheme, state := multiShapeFixture(t, "Морской бой: {0-20} {0-10}\nКатакана: {0-100}",
		`[{"cells":[[12,4],[0,0]]},{"cells":[[0],[0]]}]`)
	for _, spec := range []string{
		"Катакана: {0-100}",
		"Морской бой: {0-20}\nКатакана: {0-100}",
		"Морской бой: {0-10} {0-10}\nКатакана: {0-100}",
		"Морской бой (по 30): {0-20} {0-10}\nКатакана: {0-100}",
		"Морской бой: {0-20} {0-10}\nКатакана: {0-100}\nКатакана: {0-1}",
	} {
		_, _, err := (multi{}).Reshape(scheme, state, spec, "")
		if strings.Count(spec, "Катакана") == 2 {
			if err != nil {
				t.Errorf("%q: an added minigame is refused: %v", spec, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%q: nil error, want a refusal", spec)
		}
	}
	// An empty minigame may go, and a sorting naming a minigame that is gone
	// is refused like a typo.
	if _, _, err := (multi{}).Reshape(scheme, state, "Морской бой: {0-20} {0-10}", ""); err != nil {
		t.Errorf("dropping the empty Катакана: %v", err)
	}
	if _, _, err := (multi{}).Reshape(scheme, state, "Морской бой: {0-20} {0-10}", "total, game2"); err == nil {
		t.Error("sorting on game2 of one minigame: nil error")
	}
}
