package schemedsl

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"dope/dope/domain/games"
	"dope/dope/storage/store"
)

// bugMajorTroika is Bug Major III's Тройка, one зачёт: a written отбор of
// every troika, a Swiss stage of the best twelve, and a play-off of six whose
// three winners meet in a гранд-финал of three.
const bugMajorTroika = `
[scheme]
kind: flat
title: Отбор
written: true
themes: 9
theme_values: [1, 1, 1, 2, 2, 2, 3, 3, 3]
letters: false
sorting: [total, threes, twos, draw]
proceeding_participants: 12
---
kind: swiss
participants: 12
wins: 3
losses: 3
---
kind: single_elimination
participants: 6
themes: 8
match_size.r2: 3
themes.r2: 9
theme_values.r2: [1, 1, 1, 2, 2, 2, 3, 3, 3]
`

func troikaEntrants(n int) []store.SchemeSlot {
	out := make([]store.SchemeSlot, n)
	for i := range out {
		out[i] = store.SchemeSlot{Seed: &store.SchemeSeedRef{Basket: 1, Number: i + 1}}
	}
	return out
}

func TestBugMajorTroikaCompiles(t *testing.T) {
	scheme := compileSrc(t, bugMajorTroika, Input{Slug: "troika", Title: "Тройка", GameType: games.Troika, Entrants: troikaEntrants(20)})

	var codes []string
	for _, stage := range scheme.Stages {
		codes = append(codes, stage.Code)
	}
	t.Logf("stages: %s", strings.Join(codes, " "))

	// The отбор: one written бой of all twenty.
	qualifier := stageByCode(t, scheme, "s1")
	if qualifier.Kind != "flat" {
		t.Fatalf("s1 kind = %s", qualifier.Kind)
	}
	cfg := stageConfig(t, qualifier)
	if cfg["written"] != true || cfg["themes"].(float64) != 9 {
		t.Fatalf("s1 config = %v", cfg)
	}

	// Round 1 pairs the отбор's ranks 1–12, 2–11, … 6–7.
	r1 := stageByCode(t, scheme, "s2-r1")
	if len(r1.Matches) != 6 {
		t.Fatalf("round 1 has %d бои", len(r1.Matches))
	}
	first := r1.Matches[0].Slots
	if first[0].Reseed == nil || first[0].Reseed.Stage != "s1" || first[0].Reseed.Rank != 1 || first[1].Reseed.Rank != 12 {
		t.Fatalf("round 1 бой 1 = %+v", first)
	}

	// Round 2: two pools of six, each ranked on its own by the отбор's order
	// and dealt 1–6, 2–5, 3–4.
	pool := stageByCode(t, scheme, "s2-r2-p10")
	if !pool.Auto || pool.SeedFrom != "s1" || len(pool.Teams) != 6 {
		t.Fatalf("pool 1-0 = auto %v seedFrom %q teams %d", pool.Auto, pool.SeedFrom, len(pool.Teams))
	}
	r2 := stageByCode(t, scheme, "s2-r2")
	if len(r2.Matches) != 6 {
		t.Fatalf("round 2 has %d бои", len(r2.Matches))
	}
	if s := r2.Matches[0].Slots; s[0].Reseed.Stage != "s2-r2-p10" || s[0].Reseed.Rank != 1 || s[1].Reseed.Rank != 6 {
		t.Fatalf("round 2 бой 1 = %+v", s)
	}

	// Round 3: the 2-0 and 0-2 pools are one бой of three each, seated from the
	// бои before; the 1-1 pool of six is ranked and dealt into two бои of three.
	r3 := stageByCode(t, scheme, "s2-r3")
	if len(r3.Matches) != 4 {
		t.Fatalf("round 3 has %d бои", len(r3.Matches))
	}
	for _, m := range r3.Matches {
		if len(m.Slots) != 3 {
			t.Fatalf("round 3 %s seats %d", m.Code, len(m.Slots))
		}
	}
	if r3.Matches[0].Slots[0].FromMatch == nil {
		t.Fatalf("the 2-0 бой is seated from the бои of round 2: %+v", r3.Matches[0].Slots)
	}
	stageByCode(t, scheme, "s2-r3-p11")

	// Rounds 4 and 5, then the table.
	if r4 := stageByCode(t, scheme, "s2-r4"); len(r4.Matches) != 3 {
		t.Fatalf("round 4 has %d бои", len(r4.Matches))
	}
	if r5 := stageByCode(t, scheme, "s2-r5"); len(r5.Matches) != 1 {
		t.Fatalf("round 5 has %d бои", len(r5.Matches))
	}
	table := stageByCode(t, scheme, "s2-table")
	if table.Kind != "swiss" || table.SeedFrom != "s1" || len(table.Sources) != 5 {
		t.Fatalf("table = kind %s seedFrom %q sources %v", table.Kind, table.SeedFrom, table.Sources)
	}
	var conf struct {
		Winners map[string]int `json:"winners"`
	}
	if err := json.Unmarshal(table.Config, &conf); err != nil {
		t.Fatal(err)
	}
	if conf.Winners["s2-r3-m2"] != 2 || conf.Winners["s2-r3-m1"] != 1 || conf.Winners["s2-r1-m1"] != 1 {
		t.Fatalf("winners = %v", conf.Winners)
	}

	// The play-off: 1–6, 2–5, 3–4 by Swiss rank, then a гранд-финал of three.
	semis := stageByCode(t, scheme, "s3-r1")
	if len(semis.Matches) != 3 {
		t.Fatalf("play-off round 1 has %d бои", len(semis.Matches))
	}
	pairs := make([]string, len(semis.Matches))
	for i, m := range semis.Matches {
		pairs[i] = fmt.Sprintf("%d-%d", m.Slots[0].Reseed.Rank, m.Slots[1].Reseed.Rank)
	}
	if strings.Join(pairs, " ") != "1-6 2-5 3-4" {
		t.Fatalf("play-off pairs = %v", pairs)
	}
	var final store.SchemeStage
	for _, stage := range scheme.Stages {
		if strings.HasPrefix(stage.Code, "s3-") && stage.Code != "s3-r1" {
			final = stage
		}
	}
	if len(final.Matches) != 1 || len(final.Matches[0].Slots) != 3 {
		t.Fatalf("гранд-финал = %+v", final)
	}
	fcfg := stageConfig(t, final)
	if fcfg["themes"].(float64) != 9 {
		t.Fatalf("гранд-финал config = %v", fcfg)
	}
}
