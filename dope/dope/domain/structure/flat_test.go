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
