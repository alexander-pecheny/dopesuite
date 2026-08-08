package structure

import (
	"encoding/json"
	"fmt"
	"testing"

	"dope/dope/storage/store"
)

func bout(finished bool, questions int, slots ...SlotOutcome) MatchOutcome {
	return MatchOutcome{Finished: finished, Questions: questions, Slots: slots}
}

func seat(id int64, place float64, metrics map[string]float64) SlotOutcome {
	return SlotOutcome{Participant: id, Place: place, Metrics: metrics}
}

// A bout rule accumulates across бои; a standings rule runs once over the sums.
// The same arithmetic at the two grains gives different answers, which is the
// whole reason both exist.
func TestGrainsAreNotInterchangeable(t *testing.T) {
	rules, err := compileRules(Rules{
		Bout:      map[string]string{"points": "4 - place"},
		Standings: map[string]string{"points_share": "points / (3 * bouts)"},
	})
	if err != nil {
		t.Fatalf("compileRules: %v", err)
	}
	metrics := map[string]float64{"bouts": 2}
	for _, m := range []MatchOutcome{
		bout(true, 6, seat(1, 1, nil), seat(2, 2, nil), seat(3, 3, nil)),
		bout(true, 6, seat(1, 3, nil), seat(2, 1, nil), seat(3, 2, nil)),
	} {
		if err := rules.applyBout(m, 0, metrics); err != nil {
			t.Fatalf("applyBout: %v", err)
		}
	}
	if metrics["points"] != 4 { // 3 + 1
		t.Fatalf("points = %v, want 4", metrics["points"])
	}
	if err := rules.applyStandings(metrics); err != nil {
		t.Fatalf("applyStandings: %v", err)
	}
	if got := metrics["points_share"]; got != 4.0/6.0 {
		t.Fatalf("points_share = %v, want %v", got, 4.0/6.0)
	}
}

// разница reaches the other seats of its own бой, and reads the same whether
// the бой seats two or four.
func TestOpponentAggregates(t *testing.T) {
	rules, err := compileRules(Rules{Bout: map[string]string{
		"diff":     "taken - opp_taken",
		"gap":      "taken - opp_max_taken",
		"beat_one": "taken > opp1_taken ? 1 : 0",
	}})
	if err != nil {
		t.Fatalf("compileRules: %v", err)
	}
	duel := bout(true, 6,
		seat(1, 1, map[string]float64{"taken": 4}),
		seat(2, 2, map[string]float64{"taken": 2}))
	metrics := map[string]float64{}
	if err := rules.applyBout(duel, 0, metrics); err != nil {
		t.Fatalf("applyBout: %v", err)
	}
	if metrics["diff"] != 2 || metrics["gap"] != 2 || metrics["beat_one"] != 1 {
		t.Fatalf("duel = %v", metrics)
	}

	table := bout(true, 12,
		seat(1, 2, map[string]float64{"taken": 5}),
		seat(2, 1, map[string]float64{"taken": 7}),
		seat(3, 3, map[string]float64{"taken": 1}))
	metrics = map[string]float64{}
	if err := rules.applyBout(table, 0, metrics); err != nil {
		t.Fatalf("applyBout: %v", err)
	}
	if metrics["diff"] != -3 || metrics["gap"] != -2 || metrics["beat_one"] != 0 {
		t.Fatalf("table = %v", metrics)
	}
}

// `tied` says a бой was shared without the reader having to know that a draw
// is spelled place 1.5.
func TestTiedCountsTheSharers(t *testing.T) {
	rules, _ := compileRules(Rules{Bout: map[string]string{
		"points": "taken == 0 ? 0 : (tied > 0 ? 2 : (place == 1 ? 3 : 1))",
	}})
	draw := bout(true, 6,
		seat(1, 1.5, map[string]float64{"taken": 1}),
		seat(2, 1.5, map[string]float64{"taken": 1}))
	metrics := map[string]float64{}
	if err := rules.applyBout(draw, 0, metrics); err != nil {
		t.Fatalf("applyBout: %v", err)
	}
	if metrics["points"] != 2 {
		t.Fatalf("draw points = %v, want 2", metrics["points"])
	}
	goalless := bout(true, 6,
		seat(1, 1.5, map[string]float64{"taken": 0}),
		seat(2, 1.5, map[string]float64{"taken": 0}))
	metrics = map[string]float64{}
	_ = rules.applyBout(goalless, 0, metrics)
	if metrics["points"] != 0 {
		t.Fatalf("goalless draw points = %v, want 0", metrics["points"])
	}
}

// Rules are ordered by what they read, so a scheme's key order never matters.
func TestRuleOrderIsDerived(t *testing.T) {
	rules, err := compileRules(Rules{Standings: map[string]string{
		"zebra":  "alpha * 2",   // sorts last alphabetically, must run last
		"alpha":  "base + 1",    //
		"middle": "zebra + 100", //
	}})
	if err != nil {
		t.Fatalf("compileRules: %v", err)
	}
	metrics := map[string]float64{"base": 1}
	if err := rules.applyStandings(metrics); err != nil {
		t.Fatalf("applyStandings: %v", err)
	}
	if metrics["alpha"] != 2 || metrics["zebra"] != 4 || metrics["middle"] != 104 {
		t.Fatalf("metrics = %v", metrics)
	}
}

func TestCyclicRulesAreRejected(t *testing.T) {
	if _, err := compileRules(Rules{Standings: map[string]string{
		"a": "b + 1",
		"b": "a + 1",
	}}); err == nil {
		t.Fatal("expected a cycle error")
	}
}

func TestBadExpressionNamesItsRule(t *testing.T) {
	_, err := compileRules(Rules{Bout: map[string]string{"points": "4 - "}})
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if got := err.Error(); got[:12] != "bout.points:" {
		t.Fatalf("error %q does not name the rule", got)
	}
}

// The СИ group: nine players, three at a table, four круга, and no pair ever
// meets twice — the affine plane AG(2,3), in the reference sheets' own order.
func TestSIGroupScheduleMeetsEveryoneOnce(t *testing.T) {
	rr, ok := Kind("rr")
	if !ok {
		t.Fatal("rr not registered")
	}
	entrants := make([]store.SchemeSlot, 9)
	for i := range entrants {
		entrants[i] = store.SchemeSlot{Label: fmt.Sprintf("П%d", i+1)}
	}
	cfg, err := json.Marshal(map[string]any{
		"code": "g1", "entrants": entrants, "matchSize": 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	matches, err := rr.Schedule(cfg, nil)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(matches) != 12 {
		t.Fatalf("боёв = %d, want 12 (четыре круга по три)", len(matches))
	}
	met := map[[2]string]int{}
	played := map[string]int{}
	for _, match := range matches {
		if len(match.Slots) != 3 {
			t.Fatalf("бой %s на %d мест, want 3", match.Code, len(match.Slots))
		}
		for i, a := range match.Slots {
			played[a.Label]++
			for _, b := range match.Slots[i+1:] {
				key := [2]string{a.Label, b.Label}
				if key[0] > key[1] {
					key[0], key[1] = key[1], key[0]
				}
				met[key]++
			}
		}
	}
	for pair, times := range met {
		if times != 1 {
			t.Errorf("%s и %s встретились %d раза, want 1", pair[0], pair[1], times)
		}
	}
	if len(met) != 36 {
		t.Errorf("встреч = %d, want 36 — каждый с каждым", len(met))
	}
	for who, bouts := range played {
		if bouts != 4 {
			t.Errorf("%s сыграл %d боёв, want 4", who, bouts)
		}
	}
}

// Очки за бой на троих: 4 − место, и поделённое место платит среднее.
func TestSIGroupStandingsPayByPlace(t *testing.T) {
	rr, _ := Kind("rr")
	cfg, _ := json.Marshal(map[string]any{"matchSize": 3, "order": []string{"points", "total"}})
	results := []MatchOutcome{
		bout(true, 6,
			seat(1, 1, map[string]float64{"total": 90}),
			seat(2, 2, map[string]float64{"total": 60}),
			seat(3, 3, map[string]float64{"total": 30})),
		bout(true, 6,
			seat(1, 1.5, map[string]float64{"total": 50}),
			seat(2, 1.5, map[string]float64{"total": 50}),
			seat(3, 3, map[string]float64{"total": 10})),
	}
	ranked, err := rr.Standings(cfg, results)
	if err != nil {
		t.Fatalf("Standings: %v", err)
	}
	want := map[int64]float64{1: 3 + 2.5, 2: 2 + 2.5, 3: 1 + 1}
	for _, entry := range ranked {
		if entry.Metrics["points"] != want[entry.Participant] {
			t.Errorf("участник %d: очки %v, want %v", entry.Participant, entry.Metrics["points"], want[entry.Participant])
		}
	}
	if ranked[0].Participant != 1 || ranked[0].Rank != 1 {
		t.Errorf("первый = %d (место %d), want участник 1", ranked[0].Participant, ranked[0].Rank)
	}
}
