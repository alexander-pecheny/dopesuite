package games

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	"dope/dope/storage/store"
)

// Every registered format has a Protocol of the same code, and the
// Definition's facts about the document agree with what the Protocol
// implements: a flat format's Protocol seats its document and builds its own
// Game; a format with hand rosters says which players a bout used; a score
// metric is one the Protocol measures.
func TestEveryFormatHasAProtocolThatAgreesWithIt(t *testing.T) {
	for _, d := range All() {
		p, ok := ProtocolOf(d.Code)
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
		if d.EKBout != p.TeamBlob() && d.Code != SI {
			t.Errorf("%s: EKBout %v, TeamBlob %v", d.Code, d.EKBout, p.TeamBlob())
		}
	}
}

// A cleared flat Game is exactly a new one: the shape read back out of a
// pristine scheme builds the same scheme and document again.
func TestPristineGameSurvivesItsOwnShape(t *testing.T) {
	shapes := map[string]Shape{
		OD:    {Tours: []int{12, 15}},
		KD:    {Tours: []int{4, 4, 4}, Tables: 5},
		KSI:   {Themes: 12, Stickers: json.RawMessage(`{"types":[{"id":"x2","label":"×2","color":"#fdf66f","max":2}]}`)},
		Multi: {Minigames: []MultiGame{{Name: "Раз", Columns: []MultiColumn{{Values: []int{1, 2}}}}}, Sorting: []string{"total"}},
	}
	for _, d := range All() {
		if !d.Flat {
			continue
		}
		shape, ok := shapes[d.Code]
		if !ok {
			t.Errorf("%s: no shape to test the pristine builder with", d.Code)
			continue
		}
		scheme, state, built, err := pristineGame(d.Code, "g-1", "Игра", shape)
		if err != nil || !built {
			t.Fatalf("%s: built %v, err %v", d.Code, built, err)
		}
		again, stateAgain, _, err := pristineGame(d.Code, "g-1", "Игра", shapeOf(d.Code, string(scheme)))
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
	scheme, state, _, err := pristineGame(KD, "kd-1", "Кубок", Shape{Tours: []int{4}, Tables: 3})
	if err != nil {
		t.Fatal(err)
	}
	old := `{"players":{"1":{"name":"Аня"}},"teams":[]}`
	_, kept, err := kdFormat.Protocol.(ClearKeeper).KeepOnClear(string(scheme), old, scheme, state)
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
	if !keepsOnClear(KD) || !keepsOnClear(Multi) || keepsOnClear(OD) || keepsOnClear(KSI) {
		t.Error("only the friendship cup and Multi keep part of their document through a clear")
	}
}

// A friendship cup refuses tables that are not a prime, and more tours than
// tables, whoever builds it.
func TestFriendshipCupShapeIsChecked(t *testing.T) {
	if _, _, _, err := pristineGame(KD, "kd", "Кубок", Shape{Tours: []int{4}, Tables: 4}); err == nil {
		t.Error("four tables accepted")
	}
	if _, _, _, err := pristineGame(KD, "kd", "Кубок", Shape{Tours: []int{4, 4, 4, 4}, Tables: 3}); err == nil {
		t.Error("four tours at three tables accepted")
	}
}

// The players a bout used, by format: ids from EK's blob and Hamsa's document,
// names from brain's rows against the side's seat.
func TestUsedPlayers(t *testing.T) {
	ek, _ := usedPlayers(EK, json.RawMessage(`{"participants":{"7":{"themes":[{"players":[3],"answers":["right","","","",""]}]}}}`), nil)
	if len(ek) != 1 || ek[0].Team != 7 || ek[0].Player != 3 {
		t.Errorf("ek: %+v", ek)
	}
	brain, _ := usedPlayers(Brain, json.RawMessage(`{"teams":[{"rows":[{"player":"Аня","mark":"right"}]},{"rows":[{"player":" ","mark":"wrong"}]}]}`), []int64{11, 12})
	if len(brain) != 1 || brain[0].Team != 11 || brain[0].Name != "Аня" {
		t.Errorf("brain: %+v", brain)
	}
	if _, ok := usedPlayers(OD, json.RawMessage(`{}`), nil); ok {
		t.Error("an OD document names no players")
	}
}

// The helpers below ask a format's capability the way callers do, through
// As, and answer ok false where the format does not have it.

func pristineGame(code, slug, title string, shape Shape) (scheme, state []byte, ok bool, err error) {
	builder, ok := As[PristineBuilder](code)
	if !ok {
		return nil, nil, false, nil
	}
	scheme, state, err = builder.PristineGame(slug, title, shape)
	return scheme, state, true, err
}

func shapeOf(code, schemeJSON string) Shape {
	builder, _ := As[PristineBuilder](code)
	return builder.ShapeOf(schemeJSON)
}

func keepsOnClear(code string) bool {
	_, ok := As[ClearKeeper](code)
	return ok
}

func foldRoster(code, schemeJSON, stateJSON string, teams []RosterTeam, entryRemap map[int]int) (scheme, state []byte, ok bool, err error) {
	folder, ok := As[RosterFolder](code)
	if !ok {
		return nil, nil, false, nil
	}
	scheme, state, err = folder.FoldRoster(schemeJSON, stateJSON, teams, entryRemap)
	return scheme, state, true, err
}

func usedPlayers(code string, state json.RawMessage, seats []int64) ([]UsedPlayer, bool) {
	user, ok := As[PlayersUser](code)
	if !ok {
		return nil, false
	}
	return user.UsedPlayers(state, seats), true
}

func enteredSeats(code string, state json.RawMessage) ([]bool, bool) {
	entered, ok := As[Entered](code)
	if !ok {
		return nil, false
	}
	return entered.EnteredSeats(state), true
}
