package protocol

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	"dope/dope/domain/games"
	"dope/dope/storage/store"
)

// Every registered format has a Protocol of the same code, and the
// Definition's facts about the document agree with what the Protocol
// implements: a flat format's Protocol seats its document and builds its own
// Game; a format with hand rosters says which players a bout used; a score
// metric is one the Protocol measures.
func TestEveryFormatHasAProtocolThatAgreesWithIt(t *testing.T) {
	for _, d := range games.All() {
		p, ok := Get(d.Code)
		if !ok {
			t.Errorf("%s: no Protocol registered", d.Code)
			continue
		}
		_, seater := p.(Seater)
		_, builder := p.(PristineBuilder)
		if d.Flat != seater || d.Flat != builder {
			t.Errorf("%s: Flat %v, but Seater %v and PristineBuilder %v", d.Code, d.Flat, seater, builder)
		}
		if _, user := p.(PlayersUser); d.HandRoster && !user {
			t.Errorf("%s: hand rosters, but the Protocol does not say which players a bout used", d.Code)
		}
		if metric := store.ScoreMetric(d.Code); metric != "" && !slices.Contains(p.Metrics(nil), metric) {
			t.Errorf("%s: score metric %q is not one the Protocol measures", d.Code, metric)
		}
		if d.EKBout != p.TeamBlob() && d.Code != games.SI {
			t.Errorf("%s: EKBout %v, TeamBlob %v", d.Code, d.EKBout, p.TeamBlob())
		}
	}
}

// A cleared flat Game is exactly a new one: the shape read back out of a
// pristine scheme builds the same scheme and document again.
func TestPristineGameSurvivesItsOwnShape(t *testing.T) {
	shapes := map[string]Shape{
		games.OD:    {Tours: []int{12, 15}},
		games.KD:    {Tours: []int{4, 4, 4}, Tables: 5},
		games.KSI:   {Themes: 12, Stickers: json.RawMessage(`{"types":[{"id":"x2","label":"×2","color":"#fdf66f","max":2}]}`)},
		games.Multi: {Minigames: []games.MultiGame{{Name: "Раз", Columns: []games.MultiColumn{{Values: []int{1, 2}}}}}, Sorting: []string{"total"}},
	}
	for _, d := range games.All() {
		if !d.Flat {
			continue
		}
		shape, ok := shapes[d.Code]
		if !ok {
			t.Errorf("%s: no shape to test the pristine builder with", d.Code)
			continue
		}
		scheme, state, built, err := PristineGame(d.Code, "g-1", "Игра", shape)
		if err != nil || !built {
			t.Fatalf("%s: built %v, err %v", d.Code, built, err)
		}
		again, stateAgain, _, err := PristineGame(d.Code, "g-1", "Игра", ShapeOf(d.Code, string(scheme)))
		if err != nil {
			t.Fatalf("%s: rebuild: %v", d.Code, err)
		}
		if !bytes.Equal(scheme, again) || !bytes.Equal(state, stateAgain) {
			t.Errorf("%s: a rebuild differs:\n%s\n%s\n%s\n%s", d.Code, scheme, again, state, stateAgain)
		}
	}
}

// A friendship cup keeps its players through a clear; a Multi its guests.
func TestClearKeepsWhatTheDocumentHolds(t *testing.T) {
	scheme, state, _, err := PristineGame(games.KD, "kd-1", "Кубок", Shape{Tours: []int{4}, Tables: 3})
	if err != nil {
		t.Fatal(err)
	}
	old := `{"players":{"1":{"name":"Аня"}},"teams":[]}`
	_, kept, err := KeepOnClear(games.KD, string(scheme), old, scheme, state)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(kept, &doc); err != nil {
		t.Fatal(err)
	}
	if string(doc["players"]) != `{"1":{"name":"Аня"}}` {
		t.Errorf("players after a clear: %s", doc["players"])
	}
	if !KeepsOnClear(games.KD) || !KeepsOnClear(games.Multi) || KeepsOnClear(games.OD) || KeepsOnClear(games.KSI) {
		t.Error("only the friendship cup and Multi keep part of their document through a clear")
	}
}

// A friendship cup refuses tables that are not a prime, and more tours than
// tables, whoever builds it.
func TestFriendshipCupShapeIsChecked(t *testing.T) {
	if _, _, _, err := PristineGame(games.KD, "kd", "Кубок", Shape{Tours: []int{4}, Tables: 4}); err == nil {
		t.Error("four tables accepted")
	}
	if _, _, _, err := PristineGame(games.KD, "kd", "Кубок", Shape{Tours: []int{4, 4, 4, 4}, Tables: 3}); err == nil {
		t.Error("four tours at three tables accepted")
	}
}

// The players a bout used, by format: ids from EK's blob and Hamsa's document,
// names from brain's rows against the side's seat.
func TestUsedPlayers(t *testing.T) {
	ek, _ := UsedPlayers(games.EK, json.RawMessage(`{"participants":{"7":{"themes":[{"players":[3],"answers":["right","","","",""]}]}}}`), nil)
	if len(ek) != 1 || ek[0].Team != 7 || ek[0].Player != 3 {
		t.Errorf("ek: %+v", ek)
	}
	brain, _ := UsedPlayers(games.Brain, json.RawMessage(`{"teams":[{"rows":[{"player":"Аня","mark":"right"}]},{"rows":[{"player":" ","mark":"wrong"}]}]}`), []int64{11, 12})
	if len(brain) != 1 || brain[0].Team != 11 || brain[0].Name != "Аня" {
		t.Errorf("brain: %+v", brain)
	}
	if _, ok := UsedPlayers(games.OD, json.RawMessage(`{}`), nil); ok {
		t.Error("an OD document names no players")
	}
}
