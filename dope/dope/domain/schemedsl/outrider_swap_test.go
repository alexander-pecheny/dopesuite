package schemedsl

import (
	"encoding/json"
	"testing"

	"dope/dope/domain/games"
	"dope/dope/domain/protocol"
)

// outriderSwapTroika is the Octobearfest Троечка with the asking order turning
// in the middle of every bout: groups of six themes, a play-off of eight and a
// final of six.
const outriderSwapTroika = `
[defaults]
themes: 6
swap_outriders: true

[scheme]
title: Групповой этап
kind: roundrobin
groups: 2
group_size: 4
proceeding_participants: 2
metric: total
---
title: Плей-офф
kind: single_elimination
participants: 4
bronze: true
best_of.final: 3
themes: 8
themes.final: 6
metric: total
`

// Every bout of a scheme that says swap_outriders is built with the turn at the
// middle of its own themes: the fourth of six, the fifth of eight.
func TestSwapOutridersReachesEveryBout(t *testing.T) {
	scheme := compileSrc(t, outriderSwapTroika, Input{Slug: "troika", Title: "Тройка", GameType: games.Troika, Entrants: troikaEntrants(8)})
	p, ok := protocol.Get(games.Troika)
	if !ok {
		t.Fatal("no troika protocol")
	}
	seen := map[int]bool{}
	for _, stage := range scheme.Stages {
		if len(stage.Matches) == 0 {
			continue
		}
		cfg := stageConfig(t, stage)
		if cfg["swapOutriders"] != true {
			t.Fatalf("%s config = %v", stage.Code, cfg)
		}
		raw, err := p.EmptyState(stage.Config)
		if err != nil {
			t.Fatal(err)
		}
		var state games.TroikaState
		if err := json.Unmarshal(raw, &state); err != nil {
			t.Fatal(err)
		}
		if want := len(state.Values) / 2; state.Swap != want {
			t.Fatalf("%s: %d themes, swap at %d, want %d", stage.Code, len(state.Values), state.Swap, want)
		}
		seen[len(state.Values)] = true
	}
	if !seen[6] || !seen[8] {
		t.Fatalf("theme counts seen = %v, want both 6 and 8", seen)
	}
}
