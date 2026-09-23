package structure

import "testing"

// A flat table with its own sorting shares a rank only between seats level on
// every key — not between seats the бой placed level: ТПШ's отбор ties two
// players at Σ 270 and its «Итоги отбора» still ranks them by the 50s taken.
func TestFlatSharesRankOnlyOnEqualKeys(t *testing.T) {
	cfg := mustJSON(t, FlatConfig{Order: []string{"total", "taken50"}})
	results := []MatchOutcome{{Code: "s1-m1", Finished: true, Slots: []SlotOutcome{
		{Participant: 1, Place: 1.5, Metrics: map[string]float64{"total": 270, "taken50": 0}},
		{Participant: 2, Place: 1.5, Metrics: map[string]float64{"total": 270, "taken50": 1}},
		{Participant: 3, Place: 3.5, Metrics: map[string]float64{"total": 100, "taken50": 0}},
		{Participant: 4, Place: 3.5, Metrics: map[string]float64{"total": 100, "taken50": 0}},
	}}}
	ranked, err := flat{}.Standings(cfg, results, Inputs{})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]int, len(ranked))
	for i, entry := range ranked {
		got[i] = entry.Rank
	}
	if ranked[0].Participant != 2 || got[0] != 1 || got[1] != 2 || got[2] != 3 || got[3] != 3 {
		t.Fatalf("ranks = %v (first %d), want 1 2 3 3 with participant 2 first", got, ranked[0].Participant)
	}
	if ranked[1].Metrics["place"] != 2 {
		t.Fatalf("shown place = %v, want the rank 2, not the бой's 1.5", ranked[1].Metrics["place"])
	}
	// Without an order of its own the table is the бой's, tie shown as 1.5.
	plain, _ := flat{}.Standings(mustJSON(t, FlatConfig{}), results, Inputs{})
	if plain[0].Metrics["place"] != 1.5 || plain[1].Rank != 1 {
		t.Fatalf("plain flat: place %v rank %d, want 1.5 and a shared rank 1", plain[0].Metrics["place"], plain[1].Rank)
	}
}

// Тройка's отбор breaks a tie on Σ by the questions answered three times
// right, then twice, then by a coin (регламент IV.2.3). The coin is the game's
// lot: only the seats still level draw one, the same one every time.
func TestFlatDrawsALotOnlyForTheLevel(t *testing.T) {
	cfg := mustJSON(t, FlatConfig{Order: []string{"total", "threes", "twos", "draw"}})
	results := []MatchOutcome{{Code: "s1-m1", Finished: true, Slots: []SlotOutcome{
		{Participant: 1, Place: 2, Metrics: map[string]float64{"total": 30, "threes": 2, "twos": 1}},
		{Participant: 2, Place: 2, Metrics: map[string]float64{"total": 30, "threes": 2, "twos": 1}},
		{Participant: 3, Place: 2, Metrics: map[string]float64{"total": 30, "threes": 3, "twos": 0}},
		{Participant: 4, Place: 4, Metrics: map[string]float64{"total": 12, "threes": 0, "twos": 0}},
	}}}
	first, err := flat{}.Standings(cfg, results, Inputs{Seed: "bug-major"})
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Participant != 3 || first[3].Participant != 4 {
		t.Fatalf("order = %d … %d, want 3 first on the threes and 4 last", first[0].Participant, first[3].Participant)
	}
	ranks := []int{first[0].Rank, first[1].Rank, first[2].Rank, first[3].Rank}
	if ranks[0] != 1 || ranks[1] != 2 || ranks[2] != 3 || ranks[3] != 4 {
		t.Fatalf("ranks = %v, want distinct 1..4", ranks)
	}
	if first[0].Metrics["draw"] != 0 || first[3].Metrics["draw"] != 0 || first[1].Metrics["draw"] == 0 {
		t.Fatalf("lots = %v %v %v %v, want one only for the two level seats",
			first[0].Metrics["draw"], first[1].Metrics["draw"], first[2].Metrics["draw"], first[3].Metrics["draw"])
	}
	again, _ := flat{}.Standings(cfg, results, Inputs{Seed: "bug-major"})
	if again[1].Participant != first[1].Participant {
		t.Fatal("the coin fell differently on a recompute")
	}
}
