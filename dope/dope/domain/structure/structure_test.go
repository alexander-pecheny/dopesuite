package structure

import (
	"encoding/json"
	"testing"

	"dope/dope/storage/store"
)

func mustSchedule(t *testing.T, kind string, cfg string, results []MatchOutcome) []store.SchemeMatch {
	t.Helper()
	k, ok := Kind(kind)
	if !ok {
		t.Fatalf("kind %q not registered", kind)
	}
	matches, err := k.Schedule(json.RawMessage(cfg), results)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	return matches
}

func TestKindRegistry(t *testing.T) {
	kind, ok := Kind("rr")
	if !ok {
		t.Fatal("rr kind not registered")
	}
	if kind.Code() != "rr" {
		t.Fatalf("rr kind reports code %q", kind.Code())
	}
	if _, ok := Kind("nope"); ok {
		t.Fatal("unknown kind reported as registered")
	}
}

// Standard bracket order for 8 seeds is the recursive fold 1,8,4,5,2,7,3,6:
// undisturbed favorites meet as 1v4 / 2v3 in semis and 1v2 in the final.
func TestSingleElimScheduleEight(t *testing.T) {
	matches := mustSchedule(t, "se", `{
		"code": "po", "venue": 3, "bronze": true,
		"entrants": ["seed-1","seed-2","seed-3","seed-4","seed-5","seed-6","seed-7","seed-8"]
	}`, nil)

	byCode := map[string]store.SchemeMatch{}
	for _, m := range matches {
		byCode[m.Code] = m
	}
	if len(matches) != 8 {
		t.Fatalf("got %d matches, want 8 (4 QF + 2 SF + final + bronze)", len(matches))
	}
	qfSeeds := map[string][2]int{
		"po-r1-1": {1, 8}, "po-r1-2": {4, 5}, "po-r1-3": {2, 7}, "po-r1-4": {3, 6},
	}
	for code, want := range qfSeeds {
		m, ok := byCode[code]
		if !ok {
			t.Fatalf("missing match %s; have %v", code, codes(matches))
		}
		got := [2]int{m.Slots[0].Seed.Number, m.Slots[1].Seed.Number}
		if got != want {
			t.Errorf("%s seeds %v, want %v", code, got, want)
		}
		if m.Venue != 3 {
			t.Errorf("%s venue = %d, want 3", code, m.Venue)
		}
	}
	fromMatch := func(code string, slot int) (string, int) {
		ref := byCode[code].Slots[slot].FromMatch
		if ref == nil {
			t.Fatalf("%s slot %d has no fromMatch ref", code, slot)
		}
		return ref.Match, ref.Place
	}
	for _, tc := range []struct {
		code  string
		slot  int
		match string
		place int
	}{
		{"po-r2-1", 0, "po-r1-1", 1}, {"po-r2-1", 1, "po-r1-2", 1},
		{"po-r2-2", 0, "po-r1-3", 1}, {"po-r2-2", 1, "po-r1-4", 1},
		{"po-r3-1", 0, "po-r2-1", 1}, {"po-r3-1", 1, "po-r2-2", 1},
		{"po-r3-3p", 0, "po-r2-1", 2}, {"po-r3-3p", 1, "po-r2-2", 2},
	} {
		match, place := fromMatch(tc.code, tc.slot)
		if match != tc.match || place != tc.place {
			t.Errorf("%s slot %d ref = %s place %d, want %s place %d",
				tc.code, tc.slot, match, place, tc.match, tc.place)
		}
	}
}

// Worked example: semis 301>302, 303>304; final 303>301; bronze 302>304.
// Live variant: with only the semis finished, the two finalists share rank 1
// and the semi losers share rank 3.
func TestSingleElimStandings(t *testing.T) {
	kind, _ := Kind("se")
	cfg := json.RawMessage(`{"code":"po","bronze":true,"entrants":["seed-1","seed-2","seed-3","seed-4"]}`)
	full, err := kind.Standings(cfg, []MatchOutcome{
		h2h("po-r1-1", true, 301, 302, 5, 2, 1, 2),
		h2h("po-r1-2", true, 303, 304, 6, 1, 1, 2),
		h2h("po-r2-1", true, 303, 301, 4, 3, 1, 2),
		h2h("po-r2-3p", true, 302, 304, 2, 1, 1, 2),
	})
	if err != nil {
		t.Fatalf("Standings: %v", err)
	}
	assertRanked(t, full, []RankedEntry{
		{Rank: 1, Participant: 303}, {Rank: 2, Participant: 301},
		{Rank: 3, Participant: 302}, {Rank: 4, Participant: 304},
	})

	live, err := kind.Standings(cfg, []MatchOutcome{
		h2h("po-r1-1", true, 301, 302, 5, 2, 1, 2),
		h2h("po-r1-2", true, 303, 304, 6, 1, 1, 2),
		h2h("po-r2-1", false, 301, 303, 0, 0, 0, 0),
		h2h("po-r2-3p", false, 302, 304, 0, 0, 0, 0),
	})
	if err != nil {
		t.Fatalf("Standings: %v", err)
	}
	assertRanked(t, live, []RankedEntry{
		{Rank: 1, Participant: 301}, {Rank: 1, Participant: 303},
		{Rank: 3, Participant: 302}, {Rank: 3, Participant: 304},
	})
}

// The manual kind: its schedule is exactly the authored match list, and it
// ranks nobody — advancement out of it is by fromMatch refs alone.
func TestManualKindPassesThroughAuthoredMatches(t *testing.T) {
	matches := mustSchedule(t, "matches", `{"matches":[
		{"code":"f-1","title":"Финал","participantCount":4,"slots":["seed-1","seed-2","seed-3","seed-4"]}
	]}`, nil)
	if len(matches) != 1 || matches[0].Code != "f-1" || len(matches[0].Slots) != 4 {
		t.Fatalf("got %+v, want the single authored match f-1 with 4 slots", matches)
	}
	kind, _ := Kind("matches")
	ranked, err := kind.Standings(json.RawMessage(`{}`), nil)
	if err != nil || ranked != nil {
		t.Fatalf("manual standings = %v, %v; want nil, nil", ranked, err)
	}
}

// Worked example: two finished semis. Team 1 won m1 with total 100, team 3 won
// m2 with 90, team 4 lost m2 with 95, team 2 lost m1 with 60. Sorting by
// place_sum asc then total desc seats them 1, 3, 4, 2. Ranks are seat orders:
// always distinct, never shared.
func TestReseedStandings(t *testing.T) {
	kind, ok := Kind("reseed")
	if !ok {
		t.Fatal("reseed kind not registered")
	}
	cfg := json.RawMessage(`{"sort":[{"metric":"place_sum","dir":"asc"},{"metric":"total","dir":"desc"}]}`)
	outcome := func(code string, teamA, teamB int64, placeA, placeB float64, totalA, totalB float64) MatchOutcome {
		return MatchOutcome{Code: code, Finished: true, Slots: []SlotOutcome{
			{Participant: teamA, Place: placeA, Metrics: map[string]float64{"total": totalA}},
			{Participant: teamB, Place: placeB, Metrics: map[string]float64{"total": totalB}},
		}}
	}
	ranked, err := kind.Standings(cfg, []MatchOutcome{
		outcome("s-1", 1, 2, 1, 2, 100, 60),
		outcome("s-2", 3, 4, 1, 2, 90, 95),
	})
	if err != nil {
		t.Fatalf("Standings: %v", err)
	}
	assertRanked(t, ranked, []RankedEntry{
		{Rank: 1, Participant: 1, Metrics: map[string]float64{"place_sum": 1, "total": 100}},
		{Rank: 2, Participant: 3, Metrics: map[string]float64{"place_sum": 1, "total": 90}},
		{Rank: 3, Participant: 4, Metrics: map[string]float64{"place_sum": 2, "total": 95}},
		{Rank: 4, Participant: 2, Metrics: map[string]float64{"place_sum": 2, "total": 60}},
	})

	schedule := mustSchedule(t, "reseed", string(cfg), nil)
	if len(schedule) != 0 {
		t.Fatalf("reseed scheduled %d matches, want none", len(schedule))
	}
}

// True ties get deterministic Жребий lots from the configured seed: the order
// is stable across recomputes and independent of input order, both lots land
// in [1, 1e6], and untied teams keep draw 0.
func TestReseedDrawLots(t *testing.T) {
	kind, _ := Kind("reseed")
	cfg := json.RawMessage(`{"seed":"s1","sort":[{"metric":"place_sum","dir":"asc"},{"metric":"draw","dir":"asc"}]}`)
	tied := func(order [2]int64) []MatchOutcome {
		return []MatchOutcome{
			{Code: "m1", Finished: true, Slots: []SlotOutcome{
				{Participant: order[0], Place: 1, Metrics: map[string]float64{"total": 50}}}},
			{Code: "m2", Finished: true, Slots: []SlotOutcome{
				{Participant: order[1], Place: 1, Metrics: map[string]float64{"total": 50}},
				{Participant: 9, Place: 2, Metrics: map[string]float64{"total": 10}}}},
		}
	}
	first, err := kind.Standings(cfg, tied([2]int64{5, 6}))
	if err != nil {
		t.Fatalf("Standings: %v", err)
	}
	swapped, err := kind.Standings(cfg, tied([2]int64{6, 5}))
	if err != nil {
		t.Fatalf("Standings (swapped): %v", err)
	}
	if first[0].Participant != swapped[0].Participant || first[1].Participant != swapped[1].Participant {
		t.Errorf("lot order depends on input order: %v vs %v", first, swapped)
	}
	for _, e := range first[:2] {
		lot := e.Metrics["draw"]
		if lot < 1 || lot > 1_000_000 {
			t.Errorf("team %d lot = %v, want in [1, 1e6]", e.Participant, lot)
		}
	}
	if first[2].Participant != 9 || first[2].Metrics["draw"] != 0 {
		t.Errorf("untied team = %+v, want participant 9 with draw 0", first[2])
	}
}

func codes(matches []store.SchemeMatch) []string {
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = m.Code
	}
	return out
}

func h2h(code string, finished bool, teamA, teamB int64, takenA, takenB int, placeA, placeB int) MatchOutcome {
	return MatchOutcome{Code: code, Finished: finished, Slots: []SlotOutcome{
		{Participant: teamA, Place: float64(placeA), Metrics: map[string]float64{"taken": float64(takenA)}},
		{Participant: teamB, Place: float64(placeB), Metrics: map[string]float64{"taken": float64(takenB)}},
	}}
}

// Worked example, computed by hand: 101 beats 102 5:3, draws 103 4:4;
// 103 beats 102 6:2. Points (2/1/0): 101=3, 103=3, 102=0; the points tie
// breaks on diff (103: +4, 101: +2).
func TestRoundRobinStandings(t *testing.T) {
	kind, _ := Kind("rr")
	ranked, err := kind.Standings(json.RawMessage(`{}`), []MatchOutcome{
		h2h("g-1", true, 101, 102, 5, 3, 1, 2),
		h2h("g-2", true, 101, 103, 4, 4, 1, 1),
		h2h("g-3", true, 102, 103, 2, 6, 2, 1),
	})
	if err != nil {
		t.Fatalf("Standings: %v", err)
	}
	want := []RankedEntry{
		{Rank: 1, Participant: 103, Metrics: map[string]float64{"points": 3, "taken": 10, "conceded": 6, "diff": 4}},
		{Rank: 2, Participant: 101, Metrics: map[string]float64{"points": 3, "taken": 9, "conceded": 7, "diff": 2}},
		{Rank: 3, Participant: 102, Metrics: map[string]float64{"points": 0, "taken": 5, "conceded": 11, "diff": -6}},
	}
	assertRanked(t, ranked, want)
}

// An unfinished бой accumulates taken/conceded live but awards no points; a
// full tie on every order key shares the rank.
func TestRoundRobinStandingsUnfinishedAndSharedRank(t *testing.T) {
	kind, _ := Kind("rr")
	ranked, err := kind.Standings(json.RawMessage(`{}`), []MatchOutcome{
		h2h("g-1", true, 201, 202, 3, 3, 1, 1),
		h2h("g-2", false, 201, 202, 1, 0, 0, 0),
	})
	if err != nil {
		t.Fatalf("Standings: %v", err)
	}
	want := []RankedEntry{
		{Rank: 1, Participant: 201, Metrics: map[string]float64{"points": 1, "taken": 4, "conceded": 3, "diff": 1}},
		{Rank: 2, Participant: 202, Metrics: map[string]float64{"points": 1, "taken": 3, "conceded": 4, "diff": -1}},
	}
	ranked2, err := kind.Standings(json.RawMessage(`{}`), []MatchOutcome{
		h2h("g-1", true, 201, 202, 3, 3, 1, 1),
	})
	if err != nil {
		t.Fatalf("Standings: %v", err)
	}
	want2 := []RankedEntry{
		{Rank: 1, Participant: 201, Metrics: map[string]float64{"points": 1, "taken": 3, "conceded": 3, "diff": 0}},
		{Rank: 1, Participant: 202, Metrics: map[string]float64{"points": 1, "taken": 3, "conceded": 3, "diff": 0}},
	}
	assertRanked(t, ranked, want)
	assertRanked(t, ranked2, want2)
}

func assertRanked(t *testing.T, got, want []RankedEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Rank != want[i].Rank || got[i].Participant != want[i].Participant {
			t.Errorf("entry %d = rank %d participant %d, want rank %d participant %d",
				i, got[i].Rank, got[i].Participant, want[i].Rank, want[i].Participant)
		}
		for key, val := range want[i].Metrics {
			if got[i].Metrics[key] != val {
				t.Errorf("entry %d metric %s = %v, want %v", i, key, got[i].Metrics[key], val)
			}
		}
	}
}

// A partial round-robin is schedule data: explicit pairings replace the
// built-in schedule entirely.
func TestRoundRobinScheduleExplicitPairings(t *testing.T) {
	matches := mustSchedule(t, "rr", `{
		"code": "g",
		"entrants": ["seed-1","seed-2","seed-3","seed-4"],
		"pairings": [[[1,4]], [[2,3],[1,3]]]
	}`, nil)
	want := [][2]int{{1, 4}, {2, 3}, {1, 3}}
	if len(matches) != len(want) {
		t.Fatalf("got %d matches, want %d", len(matches), len(want))
	}
	for i, m := range matches {
		got := [2]int{m.Slots[0].Seed.Number, m.Slots[1].Seed.Number}
		if got != want[i] {
			t.Errorf("match %d pairs %v, want %v", i, got, want[i])
		}
	}
}

// Expected pairings are the KINSBF group tables (independent source: the
// generator the community's sheets use), not anything recomputed here.
func TestRoundRobinScheduleFourEntrants(t *testing.T) {
	matches := mustSchedule(t, "rr", `{
		"code": "gA", "label": "A", "venue": 2, "title": "Бой A%d",
		"entrants": ["seed-1", "seed-3", "seed-5", "seed-7"]
	}`, nil)

	wantSeeds := [][2]int{{1, 3}, {5, 7}, {1, 7}, {3, 5}, {1, 5}, {3, 7}}
	if len(matches) != len(wantSeeds) {
		t.Fatalf("got %d matches, want %d", len(matches), len(wantSeeds))
	}
	for i, m := range matches {
		wantCode := "gA-" + string(rune('1'+i))
		if m.Code != wantCode {
			t.Errorf("match %d code = %q, want %q", i, m.Code, wantCode)
		}
		if m.Venue != 2 {
			t.Errorf("match %d venue = %d, want 2", i, m.Venue)
		}
		if m.ParticipantCount != 2 || len(m.Slots) != 2 {
			t.Fatalf("match %d has %d slots (participantCount %d), want 2", i, len(m.Slots), m.ParticipantCount)
		}
		for s, want := range wantSeeds[i] {
			slot := m.Slots[s]
			if slot.Seed == nil || slot.Seed.Number != want {
				t.Errorf("match %d slot %d = %+v, want seed %d", i, s, slot, want)
			}
		}
	}
	if matches[0].Title != "Бой A1" || matches[5].Title != "Бой A6" {
		t.Errorf("titles = %q .. %q, want Бой A1 .. Бой A6", matches[0].Title, matches[5].Title)
	}
}

// Expected rounds worked out by hand with the circle method (fix entrant 1,
// rotate the rest right, pair outside-in, low position first).
func TestRoundRobinScheduleCircleMethod(t *testing.T) {
	cases := []struct {
		entrants string
		want     [][2]int
	}{
		{`["seed-1","seed-2","seed-3","seed-4","seed-5","seed-6"]`, [][2]int{
			{1, 6}, {2, 5}, {3, 4},
			{1, 5}, {4, 6}, {2, 3},
			{1, 4}, {3, 5}, {2, 6},
			{1, 3}, {2, 4}, {5, 6},
			{1, 2}, {3, 6}, {4, 5},
		}},
		{`["seed-1","seed-2","seed-3","seed-4","seed-5"]`, [][2]int{
			{2, 5}, {3, 4},
			{1, 5}, {2, 3},
			{1, 4}, {3, 5},
			{1, 3}, {2, 4},
			{1, 2}, {4, 5},
		}},
	}
	for _, tc := range cases {
		matches := mustSchedule(t, "rr", `{"code":"g","entrants":`+tc.entrants+`}`, nil)
		if len(matches) != len(tc.want) {
			t.Fatalf("%s: got %d matches, want %d", tc.entrants, len(matches), len(tc.want))
		}
		for i, m := range matches {
			got := [2]int{m.Slots[0].Seed.Number, m.Slots[1].Seed.Number}
			if got != tc.want[i] {
				t.Errorf("%s: match %d pairs %v, want %v", tc.entrants, i, got, tc.want[i])
			}
		}
	}
}
