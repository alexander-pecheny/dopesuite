package games

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
)

// The route cards VIII Octobearfest printed: card 61 at 11 tables reads
// 6, 11, 5, 10, 4, 9, 3, 8, 2, 7, 1, 6.
func TestKDTableFollowsThePrintedCards(t *testing.T) {
	var got []int
	for tour := 1; tour <= 12; tour++ {
		got = append(got, KDTable(61, tour, 11))
	}
	if want := []int{6, 11, 5, 10, 4, 9, 3, 8, 2, 7, 1, 6}; !reflect.DeepEqual(got, want) {
		t.Fatalf("card 61 at 11 tables = %v, want %v", got, want)
	}
	for tour := 1; tour <= 9; tour++ {
		if table := KDTable(7, tour, 11); table != 7 {
			t.Fatalf("joker 7 in tour %d sits at %d", tour, table)
		}
	}
}

func TestComputeKDResultsRanksPlayersByTheirTables(t *testing.T) {
	scheme, state := KDEmptyGameJSON("kd-1", "Cup", []int{2, 2}, 3, strconv.Itoa)
	var doc map[string]any
	if err := json.Unmarshal(state, &doc); err != nil {
		t.Fatal(err)
	}
	// Table 1 takes everything, table 2 one question a tour, table 3 nothing.
	doc["entries"] = [][]int{{1, 2}, {1}, {1, 2}, {1}}
	doc["completed"] = []bool{true, true, true, true}
	doc["players"] = []KDPlayer{{Card: 5, Name: "Other"}, {Card: 1, Name: "Joker One", Team: "A"}, {Card: 4, Name: "Mover"}, {Card: 2, Name: "Joker Two"}}
	state, _ = json.Marshal(doc)
	results, err := ComputeKDResults(string(scheme), string(state))
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		Name, Place string
		Total       int
		Tables      []int
		Best        []int
	}
	var got []row
	for _, p := range results.Players {
		got = append(got, row{p.Name, p.Place, p.Total, p.Tables, p.Best})
	}
	want := []row{
		{"Joker One", "1", 4, []int{1, 1}, []int{2, 0, 0}},
		{"Mover", "2", 3, []int{1, 2}, []int{1, 1, 0}},
		{"Joker Two", "3", 2, []int{2, 2}, []int{0, 2, 0}},
		{"Other", "4", 1, []int{2, 3}, []int{0, 1, 1}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("standings = %+v\nwant %+v", got, want)
	}
	if results.Tables != 3 || results.Players[0].Team != "A" {
		t.Fatalf("tables %d, team %q", results.Tables, results.Players[0].Team)
	}
}

func TestComputeKDResultsSharesAPlaceOnlyWhenEverythingIsEqual(t *testing.T) {
	scheme, state := KDEmptyGameJSON("kd-1", "Cup", []int{1, 1}, 2, strconv.Itoa)
	var doc map[string]any
	_ = json.Unmarshal(state, &doc)
	// Card 3 sits at 1 then 2; card 4 at 2 then 1. Both take one question.
	doc["entries"] = [][]int{{1}, {1}}
	doc["completed"] = []bool{true, true}
	doc["players"] = []KDPlayer{{Card: 4, Name: "B"}, {Card: 3, Name: "A"}}
	state, _ = json.Marshal(doc)
	results, err := ComputeKDResults(string(scheme), string(state))
	if err != nil {
		t.Fatal(err)
	}
	if p := results.Players; p[0].Name != "A" || p[0].Place != "1–2" || p[1].Place != "1–2" {
		t.Fatalf("tied players = %+v", p)
	}
}

// The players read in both shapes, and an entry the page would not show is
// left out: a card that is no whole number from 1, one past n², a second
// holder, an empty name, a freed card.
func TestKDPlayersReadsBothShapesAndSkipsBadEntries(t *testing.T) {
	keyed := json.RawMessage(`{"5":{"name":" Boris ","team":"B"},"1":{"name":"Anna"},"2":null,"07":{"name":"Zero-led"},"x":{"name":"X"},"10":{"name":"Past"},"3":{"name":""}}`)
	if got, want := KDPlayers(keyed, 3), []KDPlayer{{Card: 1, Name: "Anna"}, {Card: 5, Name: "Boris", Team: "B"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("keyed = %+v, want %+v", got, want)
	}
	list := json.RawMessage(`[{"card":"2","name":"String"},{"card":0,"name":"Zero"},{"card":4,"name":"Good"},{"card":4,"name":"Twice"},{"card":1.5,"name":"Half"},{"card":1,"name":"Joker"}]`)
	if got, want := KDPlayers(list, 3), []KDPlayer{{Card: 1, Name: "Joker"}, {Card: 4, Name: "Good"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("list = %+v, want %+v", got, want)
	}
	if got := KDPlayers(json.RawMessage(`"nonsense"`), 3); len(got) != 0 {
		t.Fatalf("nonsense = %+v", got)
	}
}

// A joker is a card whose route never moves: cards 1…n, and the n cards
// after every n² that repeat them.
func TestKDJokerIsEveryCardThatStaysAtOneTable(t *testing.T) {
	const n = 3
	for card := 1; card <= 3*n*n; card++ {
		stays := true
		for tour := 2; tour <= n; tour++ {
			stays = stays && KDTable(card, tour, n) == KDTable(card, 1, n)
		}
		if KDJoker(card, n) != stays {
			t.Errorf("card %d: joker %v, stays %v", card, KDJoker(card, n), stays)
		}
	}
}

func TestIsPrime(t *testing.T) {
	for n, want := range map[int]bool{0: false, 1: false, 2: true, 9: false, 11: true, 13: true, 25: false, 29: true} {
		if IsPrime(n) != want {
			t.Errorf("IsPrime(%d) = %v", n, !want)
		}
	}
}
