package schemedsl

import (
	"testing"

	"dope/dope/storage/store"
)

func hamsaInput(entrants int) Input {
	in := Input{Slug: "hamsa", Title: "Хамса", GameType: "hamsa"}
	for i := 0; i < entrants; i++ {
		in.Entrants = append(in.Entrants, store.SchemeSlot{
			Seed: &store.SchemeSeedRef{Basket: 1, Number: i + 1},
		})
	}
	return in
}

const hamsaSrc = `
[defaults]
venues: [А, Б, В]

[scheme]
title: Групповой этап
kind: placement
participants: 12
match_size: 4
rounds: 2
title.r1: Игра №1
title.r2: Игра №2
proceeding_participants: 4
sorting: [place_sum, total, first, seed]
---
title: Финал
kind: flat
participants: 4
reseed: true
stats_from: [s1]
sorting: [place_sum, total, first, seed]
shootout: true
`

// «Площадка А: команды, занявшие 1–4 места в КСИ; Б: 5–8; В: 9–12» — the
// opening Round is dealt in straight bands of the seed, not snaked.
func TestPlacementDealsTheFirstBlockRoundInStraightBands(t *testing.T) {
	scheme := compileSrc(t, hamsaSrc, hamsaInput(12))
	stage := stageByCode(t, scheme, "s1-r1")
	if stage.Title != "Групповой этап. Игра №1" {
		t.Fatalf("title = %q", stage.Title)
	}
	if len(stage.Matches) != 3 {
		t.Fatalf("matches = %d, want 3 tables", len(stage.Matches))
	}
	for table, want := range [][]int{{1, 2, 3, 4}, {5, 6, 7, 8}, {9, 10, 11, 12}} {
		match := stage.Matches[table]
		if match.Venue != table+1 {
			t.Errorf("table %d plays at venue %d", table+1, match.Venue)
		}
		if len(match.Slots) != 4 {
			t.Fatalf("table %d seats %d", table+1, len(match.Slots))
		}
		for seat, rank := range want {
			if got := match.Slots[seat].Seed; got == nil || got.Number != rank {
				t.Errorf("table %d seat %d = %+v, want seed %d", table+1, seat, match.Slots[seat], rank)
			}
		}
	}
}

// «Площадка А: победители групп; Б: вторые места; В: третьи места» — and the
// fourth places are a blind Draw, so those seats name their candidates and
// stand empty.
func TestPlacementSeatsPlacesForwardAndDrawsTheRest(t *testing.T) {
	scheme := compileSrc(t, hamsaSrc, hamsaInput(12))
	stage := stageByCode(t, scheme, "s1-r2")
	if stage.Title != "Групповой этап. Игра №2" {
		t.Fatalf("title = %q", stage.Title)
	}
	for table := 0; table < 3; table++ {
		match := stage.Matches[table]
		if len(match.Slots) != 4 {
			t.Fatalf("table %d seats %d", table+1, len(match.Slots))
		}
		for from := 0; from < 3; from++ {
			slot := match.Slots[from]
			if slot.FromMatch == nil || slot.FromMatch.Place != table+1 {
				t.Fatalf("table %d seat %d = %+v, want place %d carried forward", table+1, from, slot, table+1)
			}
			if want := "s1-r1-m" + string(rune('1'+from)); slot.FromMatch.Match != want {
				t.Errorf("table %d seat %d comes from %s, want %s", table+1, from, slot.FromMatch.Match, want)
			}
		}
		draw := match.Slots[3].Draw
		if draw == nil {
			t.Fatalf("table %d has no Draw seat: %+v", table+1, match.Slots[3])
		}
		if draw.Code != match.Code+"-d1" {
			t.Errorf("draw code = %q", draw.Code)
		}
		if len(draw.Candidates) != 3 {
			t.Fatalf("draw candidates = %+v, want the three fourth places", draw.Candidates)
		}
		for i, candidate := range draw.Candidates {
			if candidate.Place != 4 || candidate.Match != "s1-r1-m"+string(rune('1'+i)) {
				t.Errorf("candidate %d = %+v", i, candidate)
			}
		}
	}
}

// The Block is one ranking scope: «наименьшая сумма мест, занятых командами в
// обеих играх ГЭ». So it holds no Matches of its own and names both Игры.
func TestPlacementRanksBothBlockRoundsInOneTable(t *testing.T) {
	scheme := compileSrc(t, hamsaSrc, hamsaInput(12))
	table := stageByCode(t, scheme, "s1-total")
	if table.Kind != "placement" {
		t.Fatalf("kind = %q", table.Kind)
	}
	if len(table.Matches) != 0 {
		t.Fatalf("the table plays %d бои of its own", len(table.Matches))
	}
	if len(table.Sources) != 2 || table.Sources[0] != "s1-r1" || table.Sources[1] != "s1-r2" {
		t.Fatalf("sources = %v", table.Sources)
	}
	config := stageConfig(t, table)
	order, ok := config["order"].([]any)
	if !ok || len(order) != 4 || order[0] != "place_sum" || order[3] != "seed" {
		t.Fatalf("order = %v", config["order"])
	}
}

// The Финал takes the four best of the ГЭ through a reseed, and its own
// sorting describes that Edge rather than the бой's table.
func TestHamsaFinalReseatsFromTheBlockTable(t *testing.T) {
	scheme := compileSrc(t, hamsaSrc, hamsaInput(12))
	reseed := stageByCode(t, scheme, "s2-reseed")
	if len(reseed.Teams) != 4 {
		t.Fatalf("reseed contenders = %d, want the four proceeding", len(reseed.Teams))
	}
	for rank, slot := range reseed.Teams {
		if slot.Reseed == nil || slot.Reseed.Stage != "s1-total" || slot.Reseed.Rank != rank+1 {
			t.Fatalf("contender %d = %+v", rank, slot)
		}
	}
	if len(reseed.Sources) != 2 || reseed.Sources[0] != "s1-r1" {
		t.Fatalf("reseed sources = %v", reseed.Sources)
	}
	final := stageByCode(t, scheme, "s2")
	if len(final.Matches) != 1 || len(final.Matches[0].Slots) != 4 {
		t.Fatalf("final = %+v", final.Matches)
	}
	for seat, slot := range final.Matches[0].Slots {
		if slot.Reseed == nil || slot.Reseed.Stage != "s2-reseed" || slot.Reseed.Rank != seat+1 {
			t.Fatalf("final seat %d = %+v", seat, slot)
		}
	}
	if config := stageConfig(t, final); config["shootout"] != true {
		t.Fatalf("the Финал allows a перестрелка: %v", config)
	}
}

// The shape is not wired to this one tournament: eight teams, four to a table,
// three Игры leaves two derived seats and two drawn ones per table.
func TestPlacementScalesToAnotherShape(t *testing.T) {
	scheme := compileSrc(t, `
[scheme]
kind: placement
participants: 8
match_size: 4
rounds: 3
`, hamsaInput(8))
	for _, code := range []string{"s1-r1", "s1-r2", "s1-r3"} {
		stage := stageByCode(t, scheme, code)
		if len(stage.Matches) != 2 {
			t.Fatalf("%s has %d tables, want 2", code, len(stage.Matches))
		}
	}
	match := stageByCode(t, scheme, "s1-r2").Matches[0]
	derived, drawn := 0, 0
	for _, slot := range match.Slots {
		if slot.FromMatch != nil {
			derived++
		}
		if slot.Draw != nil {
			drawn++
			if len(slot.Draw.Candidates) != 4 {
				t.Errorf("draw candidates = %d, want places 3 and 4 of both tables", len(slot.Draw.Candidates))
			}
		}
	}
	if derived != 2 || drawn != 2 {
		t.Fatalf("seats = %d derived, %d drawn", derived, drawn)
	}
}

// A table cannot seat place k of every table when there are more tables than
// seats, so the block says so rather than compiling something nobody can play.
func TestPlacementRefusesMoreTablesThanSeats(t *testing.T) {
	doc, err := Parse(`
[scheme]
kind: placement
participants: 12
match_size: 2
rounds: 2
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(doc, hamsaInput(12)); err == nil {
		t.Fatal("six tables of two compiled")
	}
}
