package schemedsl

import (
	"fmt"
	"testing"

	"dope/dope/storage/store"
)

func troikaInput(entrants int) Input {
	in := Input{Slug: "troika", Title: "Тройка", GameType: "troika"}
	for i := 0; i < entrants; i++ {
		in.Entrants = append(in.Entrants, store.SchemeSlot{
			Seed: &store.SchemeSeedRef{Basket: 1, Number: i + 1},
		})
	}
	return in
}

// Троечка's финал and матч за 3-е место are seeded straight out of the 3rd
// групповой этап — победители групп в финал, вторые места в бронзу — with no
// semifinal to lose. The se block therefore takes participants: 2 and draws
// its bronze pair from the incoming Edge, consuming four.
const troikaFinalSrc = `
[defaults]
themes: 6

[scheme]
kind: roundrobin
groups: 2
group_size: 4
proceeding_participants: 2
themes: 8
---
kind: single_elimination
participants: 2
bronze: true
best_of.final: 3
best_of.bronze: 3
points: [1, 0.5, 0]
standings.rating: points + taken / 20
sorting: [rating]
`

func TestTroikaFinalSeatsFromTheGroupsWithoutASemifinal(t *testing.T) {
	scheme := compileSrc(t, troikaFinalSrc, troikaInput(8))

	final := stageByCode(t, scheme, "s2-final")
	if len(final.Matches) != 3 {
		t.Fatalf("final matches = %d, want a series of 3", len(final.Matches))
	}
	for k, match := range final.Matches {
		if match.Title != fmt.Sprintf("Финал. Бой %d", k+1) {
			t.Fatalf("final match %d title = %q", k, match.Title)
		}
		if match.Slots[0].Reseed.Stage != "s1-g1" || match.Slots[0].Reseed.Rank != 1 ||
			match.Slots[1].Reseed.Stage != "s1-g2" || match.Slots[1].Reseed.Rank != 1 {
			t.Fatalf("final match %d slots: %+v %+v", k, match.Slots[0], match.Slots[1])
		}
	}

	bronze := stageByCode(t, scheme, "s2-bronze")
	if len(bronze.Matches) != 3 {
		t.Fatalf("bronze matches = %d, want a series of 3", len(bronze.Matches))
	}
	bm := bronze.Matches[0].Slots
	if bm[0].Reseed.Stage != "s1-g1" || bm[0].Reseed.Rank != 2 ||
		bm[1].Reseed.Stage != "s1-g2" || bm[1].Reseed.Rank != 2 {
		t.Fatalf("bronze slots: %+v %+v", bm[0], bm[1])
	}
	if bronze.Position >= final.Position {
		t.Fatalf("bronze at %d, final at %d: bronze is played first", bronze.Position, final.Position)
	}
}

// The группа blocks write the регламент verbatim: 1 / 0.5 / 0 плюс очки/50,
// ranked on the рейтинговый балл, then личная встреча, забитые, разница.
const troikaGroupSrc = `
[scheme]
kind: roundrobin
groups: 2
group_size: 4
proceeding_participants: 2
themes: 8
metric: total
points: [1, 0.5, 0]
standings.rating: points + taken / 50
sorting: [rating, h2h, taken, diff]
`

func TestTroikaGroupCarriesFractionalPointsAndItsRatingRule(t *testing.T) {
	scheme := compileSrc(t, troikaGroupSrc, troikaInput(8))
	config := stageConfig(t, stageByCode(t, scheme, "s1-g1"))

	points, ok := config["points"].(map[string]any)
	if !ok {
		t.Fatalf("points = %#v", config["points"])
	}
	if points["win"] != float64(1) || points["draw"] != 0.5 || points["loss"] != float64(0) {
		t.Fatalf("points = %+v, want 1 / 0.5 / 0", points)
	}
	if config["metric"] != "total" {
		t.Fatalf("metric = %v, want total", config["metric"])
	}
	rules, ok := config["rules"].(map[string]any)
	if !ok {
		t.Fatalf("rules = %#v", config["rules"])
	}
	standings := rules["standings"].(map[string]any)
	if standings["rating"] != "points + taken / 50" {
		t.Fatalf("standings.rating = %v", standings["rating"])
	}
	order, _ := config["order"].([]any)
	if len(order) != 4 || order[0] != "rating" || order[1] != "h2h" {
		t.Fatalf("order = %v", order)
	}
}
