package games

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

// «Ассорти» as it was actually played: two мини-игры on 68 teams — a медиа-
// эрудит of eight темы at 10..50 with minuses, and a песенный конкурс of 72
// columns worth a балл each — each normalised to a hundred and then added.
//
// The workbook is the oracle. Its own formulas computed the Σ of every team in
// each мини-игра, then «сколько от лучшего» out of a hundred, then the Итог;
// dope is handed the same cells and has to arrive at the same numbers. The two
// мини-игры differ in scale by a factor of twenty, which is the whole reason
// the format normalises at all.
func TestAssortiSheetIsReproduced(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/assorti2025/assorti.json")
	if err != nil {
		t.Skip("нет набора: scripts/multi/read-assorti-sheets.py")
	}
	var fixture struct {
		Spec           string               `json:"spec"`
		Participants   []KSIParticipant     `json:"participants"`
		Declined       []string             `json:"declined"`
		Games          [][][]int            `json:"games"`
		MinigameTotals []map[string]int     `json:"minigameTotals"`
		MinigamePoints []map[string]float64 `json:"minigamePoints"`
		Overall        []struct {
			Place float64 `json:"place"`
			Name  string  `json:"name"`
			Total float64 `json:"total"`
		} `json:"overall"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}

	minigames, err := ParseMultiGames(fixture.Spec)
	if err != nil {
		t.Fatalf("ParseMultiGames: %v", err)
	}
	if len(minigames) != 2 || !minigames[0].Normalized || !minigames[1].Normalized {
		t.Fatalf("мини-игры = %+v", minigames)
	}

	declined := map[string]bool{}
	for _, name := range fixture.Declined {
		declined[name] = true
	}
	state := map[string]any{"participants": fixture.Participants}
	marks := map[string]bool{}
	for _, p := range fixture.Participants {
		if declined[p.Name] {
			marks[KSIDeclinedKey(p.Number, p.Name)] = true
		}
	}
	state["declined"] = marks
	grids := make([]map[string]any, len(fixture.Games))
	for i, grid := range fixture.Games {
		grids[i] = map[string]any{"cells": grid}
	}
	state["games"] = grids

	scheme, err := json.Marshal(map[string]any{"minigames": minigames})
	if err != nil {
		t.Fatal(err)
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	ranked, err := ComputeMultiResults(string(scheme), string(stateJSON))
	if err != nil {
		t.Fatalf("ComputeMultiResults: %v", err)
	}

	byName := map[string]MultiResultsTeam{}
	for _, row := range ranked {
		byName[fixture.Participants[row.Index].Name] = row
	}
	if len(ranked) != len(fixture.Participants)-len(fixture.Declined) {
		t.Fatalf("в зачёте %d команд, а вне зачёта %d из %d",
			len(ranked), len(fixture.Declined), len(fixture.Participants))
	}

	// Every мини-игра's Σ, exactly as the sheet added the same cells up.
	for game, totals := range fixture.MinigameTotals {
		for name, want := range totals {
			row, ok := byName[name]
			if !ok {
				continue // вне зачёта: the sheet keeps a Σ for them, dope no place
			}
			if row.Raw[game] != want {
				t.Errorf("%s, мини-игра %d: Σ %d, лист говорит %d", name, game+1, row.Raw[game], want)
			}
		}
	}
	// And «сколько от лучшего» — the two-decimal number the sheet printed.
	for game, points := range fixture.MinigamePoints {
		for name, want := range points {
			row, ok := byName[name]
			if !ok {
				continue
			}
			if math.Abs(row.Games[game]-want) > 0.011 {
				t.Errorf("%s, мини-игра %d: очки %.4f, лист говорит %.2f", name, game+1, row.Games[game], want)
			}
		}
	}
	// The Итог, and the order it puts the фест in.
	for _, want := range fixture.Overall {
		row, ok := byName[want.Name]
		if !ok {
			t.Errorf("%s есть в общей таблице, а в зачёте нет", want.Name)
			continue
		}
		if math.Abs(row.Total-want.Total) > 0.011 {
			t.Errorf("%s: итог %.4f, лист говорит %.2f", want.Name, row.Total, want.Total)
		}
	}
	// The sheet numbers straight through 1..N and dope shares a place, so the
	// order is what is held — every team ahead of the next on the sheet is at
	// least level with it here.
	for i := 1; i < len(fixture.Overall); i++ {
		before, after := byName[fixture.Overall[i-1].Name], byName[fixture.Overall[i].Name]
		if before.Place > after.Place {
			t.Errorf("лист ставит %s выше %s, а места %g и %g",
				fixture.Overall[i-1].Name, fixture.Overall[i].Name, before.Place, after.Place)
		}
	}
	t.Logf("«Ассорти»: %d команд в зачёте, %d вне, обе мини-игры и общая таблица сошлись",
		len(ranked), len(fixture.Declined))
}
