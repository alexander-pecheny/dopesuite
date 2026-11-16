package structure

import (
	"encoding/json"
	"fmt"
	"testing"
)

// The regulations' own example of 3.1.7: three teams on 4 points, four on 2,
// one on 0. The odd group sends its middle down, the even group pairs that
// one with its last and sends its first down, and the rest meet first with
// last.
func TestRegulationPairingAcrossGroups(t *testing.T) {
	ids := []int64{1, 2, 3, 4, 5, 6, 7, 8}
	points := map[int64]float64{1: 4, 2: 4, 3: 4, 4: 2, 5: 2, 6: 2, 7: 2, 8: 0}
	got := fmt.Sprint(regulationPairing(ids, points))
	if want := "[[1 3] [2 7] [5 6] [4 8]]"; got != want {
		t.Fatalf("pairs = %s, want %s", got, want)
	}
}

func swissBout(code string, a, b int64, takenA, takenB float64) MatchOutcome {
	place := func(x, y float64) float64 {
		switch {
		case x > y:
			return 1
		case x < y:
			return 2
		}
		return 1.5
	}
	return MatchOutcome{Code: code, Finished: true, Slots: []SlotOutcome{
		{Participant: a, Place: place(takenA, takenB), Metrics: map[string]float64{"taken": takenA}},
		{Participant: b, Place: place(takenB, takenA), Metrics: map[string]float64{"taken": takenB}},
	}}
}

// emptyTable is a бой against the empty table as the store returns it: the
// empty seat is there, unplaced.
func emptyTable(code string, team int64, taken float64) MatchOutcome {
	place := 1.5
	if taken > 0 {
		place = 1
	}
	return MatchOutcome{Code: code, Finished: true, Slots: []SlotOutcome{
		{Participant: team, Place: place, Metrics: map[string]float64{"taken": taken}},
		{Participant: 0, Place: 0},
	}}
}

var vbConfig = SwissClassicConfig{Points: RRPoints{Win: 2, Draw: 1, Loss: 0}, ZeroDraw: 0, Order: swissClassicOrder}

// A win pays two, a draw with answers one, a 0:0 nothing; the empty table
// pays as a бой does and counts in nobody's Buchholz.
func TestSwissTablePointsAndBuchholz(t *testing.T) {
	results := []MatchOutcome{
		swissBout("r1-m1", 1, 2, 3, 1),
		swissBout("r1-m2", 3, 4, 2, 2),
		emptyTable("r1-m3", 5, 1),
		swissBout("r2-m1", 1, 3, 0, 0),
		swissBout("r2-m2", 2, 5, 0, 4),
		emptyTable("r2-m3", 4, 0),
	}
	seeds := map[int64]float64{1: 1, 2: 2, 3: 3, 4: 4, 5: 5}
	table := swissRecords(vbConfig, results, seeds, nil).table(vbConfig.Order)
	byID := map[int64]map[string]float64{}
	var order []int64
	for _, e := range table {
		byID[e.Participant] = e.Metrics
		order = append(order, e.Participant)
	}
	if got := fmt.Sprint(order); got != "[5 1 3 4 2]" {
		t.Fatalf("table = %s", got)
	}
	want := map[int64][3]float64{ // points, buchholz, byes
		1: {2, 0 + 1, 0},
		2: {0, 2 + 4, 0},
		3: {1, 1 + 2, 0},
		4: {1, 1, 1},
		5: {4, 0, 1},
	}
	for id, w := range want {
		m := byID[id]
		if m["points"] != w[0] || m["buchholz"] != w[1] || m["byes"] != w[2] {
			t.Errorf("team %d: points %v buchholz %v byes %v, want %v", id, m["points"], m["buchholz"], m["byes"], w)
		}
	}
}

// With an odd field the lowest team that has not sat out does; a pair that
// already met is broken up even when that costs a group crossing.
func TestPairingByeAndNoRematch(t *testing.T) {
	results := []MatchOutcome{
		swissBout("r1-m1", 1, 3, 2, 1),
		swissBout("r1-m2", 2, 4, 2, 1),
		emptyTable("r1-m3", 5, 1),
	}
	in := Inputs{Seeds: map[int64]float64{1: 1, 2: 2, 3: 3, 4: 4, 5: 5}}
	for id := int64(1); id <= 5; id++ {
		in.Contenders = append(in.Contenders, Contender{Participant: id})
	}
	cfg, _ := json.Marshal(vbConfig)
	ranked, err := swissPairing{}.Standings(cfg, results, in)
	if err != nil {
		t.Fatal(err)
	}
	var seats []int64
	for _, e := range ranked {
		seats = append(seats, e.Participant)
	}
	// 5 has sat out, so 4, the lowest of the rest, does. 1 and 2 lead on
	// two points; 5 also has two but fewer answers, so the group is 1, 2, 5,
	// odd: 2 goes down to meet 3, and 1 meets 5.
	if got := fmt.Sprint(seats); got != "[1 5 2 3 4]" {
		t.Fatalf("seats = %s", got)
	}
}

func TestPairingAvoidsRematch(t *testing.T) {
	// Four teams level after one round of draws: first meets last would
	// repeat 1–4 and 2–3, so the pairing goes round them.
	results := []MatchOutcome{
		swissBout("r1-m1", 1, 4, 1, 1),
		swissBout("r1-m2", 2, 3, 1, 1),
	}
	in := Inputs{Seeds: map[int64]float64{1: 1, 2: 2, 3: 3, 4: 4}}
	for id := int64(1); id <= 4; id++ {
		in.Contenders = append(in.Contenders, Contender{Participant: id})
	}
	cfg, _ := json.Marshal(vbConfig)
	ranked, err := swissPairing{}.Standings(cfg, results, in)
	if err != nil {
		t.Fatal(err)
	}
	met := map[[2]int64]bool{{1, 4}: true, {4, 1}: true, {2, 3}: true, {3, 2}: true}
	for i := 0; i < len(ranked); i += 2 {
		a, b := ranked[i].Participant, ranked[i+1].Participant
		if met[[2]int64{a, b}] {
			t.Fatalf("pair %d–%d meets again (seats %v)", a, b, ranked)
		}
	}
}

// Seven rounds with varied results, at an odd and an even field: two teams
// meet twice only when no pairing of that round avoids it (3.1.7), and
// nobody sits out twice.
func TestPairingSevenRounds(t *testing.T) {
	for _, teams := range []int64{9, 16, 23} {
		cfg, _ := json.Marshal(vbConfig)
		in := Inputs{Seeds: map[int64]float64{}}
		for id := int64(1); id <= teams; id++ {
			in.Seeds[id] = float64(id)
			in.Contenders = append(in.Contenders, Contender{Participant: id})
		}
		var results []MatchOutcome
		met := map[[2]int64]bool{}
		byes := map[int64]int{}
		for round := int64(1); round <= 7; round++ {
			ranked, err := swissPairing{}.Standings(cfg, results, in)
			if err != nil {
				t.Fatal(err)
			}
			var playing []int64
			for i := 0; i+1 < len(ranked); i += 2 {
				playing = append(playing, ranked[i].Participant, ranked[i+1].Participant)
			}
			avoidable := rematchFree(playing, met)
			for i := 0; i+1 < len(playing); i += 2 {
				a, b := playing[i], playing[i+1]
				if met[[2]int64{a, b}] && avoidable {
					t.Fatalf("%d teams, round %d: %d–%d meet again though a pairing without it exists", teams, round, a, b)
				}
				met[[2]int64{a, b}], met[[2]int64{b, a}] = true, true
				// A strength-ish result: the lower id takes more, now and then a draw.
				ta, tb := float64((a*7+round)%4), float64((b*5+round)%4)
				results = append(results, swissBout(fmt.Sprintf("r%d-m%d", round, i), a, b, ta, tb))
			}
			if len(ranked)%2 == 1 {
				out := ranked[len(ranked)-1].Participant
				if byes[out]++; byes[out] > 1 {
					t.Fatalf("%d teams, round %d: %d sits out twice", teams, round, out)
				}
				results = append(results, emptyTable(fmt.Sprintf("r%d-bye", round), out, float64(round%2)))
			}
		}
	}
}

// rematchFree reports whether the teams can be paired with no pair that met.
func rematchFree(ids []int64, met map[[2]int64]bool) bool {
	if len(ids) == 0 {
		return true
	}
	for j := 1; j < len(ids); j++ {
		if met[[2]int64{ids[0], ids[j]}] {
			continue
		}
		rest := append(append([]int64(nil), ids[1:j]...), ids[j+1:]...)
		if rematchFree(rest, met) {
			return true
		}
	}
	return false
}
