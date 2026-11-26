package games

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"
	"strings"

	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
)

// Reshaper is a format whose shape the host may write again on a Game already
// created: Multi's minigames, when the organizers drop one or split a
// minigame into its tasks. The shape is the text the creation form took, so
// the settings page shows it back and the host edits it as typed.
type Reshaper interface {
	// Shape is the Game's shape as the settings page shows it: the minigames'
	// spec and the sorting.
	Shape(schemeJSON string) (spec, sorting string)
	// Reshape writes a new shape into the scheme and fits the document to it.
	// Nothing entered may be lost: a change that would drop a value, or leave
	// one outside its task's new domain, is refused.
	Reshape(schemeJSON, stateJSON, spec, sorting string) (scheme, state []byte, err error)
}

// MultiShapeKey is the state's stamp of the minigames it is shaped for. A
// reshape writes it, and a page holding the old scheme tells by it that its
// columns moved and fetches the scheme again.
const MultiShapeKey = "shape"

func (multi) Shape(schemeJSON string) (string, string) {
	var scheme MultiScheme
	_ = json.Unmarshal([]byte(schemeJSON), &scheme)
	return FormatMultiGames(scheme.Minigames), strings.Join(scheme.Sorting, ", ")
}

// Reshape keeps a minigame's cells under its name, so dropping or reordering
// minigames moves nothing, and keeps a task's cells under its position within
// the minigame. A renamed minigame is a new one: its old cells must be empty.
func (multi) Reshape(schemeJSON, stateJSON, spec, sorting string) ([]byte, []byte, error) {
	minigames, err := ParseMultiGames(spec)
	if err != nil {
		return nil, nil, err
	}
	order, err := ParseMultiSorting(minigames, sorting)
	if err != nil {
		return nil, nil, err
	}
	var oldScheme MultiScheme
	if err := json.Unmarshal([]byte(schemeJSON), &oldScheme); err != nil {
		return nil, nil, err
	}
	state := map[string]json.RawMessage{}
	if strings.TrimSpace(stateJSON) != "" {
		if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
			return nil, nil, err
		}
	}
	var grids []struct {
		Cells [][]int `json:"cells"`
	}
	if raw, ok := state["games"]; ok {
		if err := json.Unmarshal(raw, &grids); err != nil {
			return nil, nil, err
		}
	}
	old := make([][][]int, len(oldScheme.Minigames))
	for g := range old {
		if g < len(grids) {
			old[g] = grids[g].Cells
		}
	}
	cells, err := reshapeMultiCells(oldScheme.Minigames, old, minigames)
	if err != nil {
		return nil, nil, err
	}
	return writeMultiShape(schemeJSON, state, minigames, order, cells)
}

// reshapeMultiCells deals the old grids out to the new minigames, matched by
// name, and refuses when a value would be lost on the way.
func reshapeMultiCells(oldGames []MultiGame, old [][][]int, games []MultiGame) ([][][]int, error) {
	used := make([]bool, len(oldGames))
	cells := make([][][]int, len(games))
	for g, game := range games {
		from := -1
		for o, oldGame := range oldGames {
			if !used[o] && oldGame.Name == game.Name {
				from = o
				break
			}
		}
		if from < 0 {
			cells[g] = [][]int{}
			continue
		}
		used[from] = true
		rows, err := fitMultiRows(game, old[from])
		if err != nil {
			return nil, err
		}
		cells[g] = rows
	}
	for g, game := range oldGames {
		if !used[g] && multiGridHolds(old[g]) {
			return nil, corei18n.User(dopestrings.Default.Games.Multi.ReshapeDropsGame(game.Name))
		}
	}
	return cells, nil
}

// fitMultiRows is a minigame's old grid at its new width, each value under
// its task's position, refused when a value has no task to go to or is not
// one the task takes now.
func fitMultiRows(game MultiGame, old [][]int) ([][]int, error) {
	rows := make([][]int, len(old))
	for p, row := range old {
		rows[p] = make([]int, len(game.Columns))
		for c, value := range row {
			if value == 0 {
				continue
			}
			if c >= len(game.Columns) {
				return nil, corei18n.User(dopestrings.Default.Games.Multi.ReshapeDropsTask(game.Name, strconv.Itoa(c+1)))
			}
			if !slices.Contains(game.Columns[c].Values, value) {
				return nil, corei18n.User(dopestrings.Default.Games.Multi.ReshapeOutOfDomain(game.Name, strconv.Itoa(c+1), strconv.Itoa(value)))
			}
			rows[p][c] = value
		}
	}
	return rows, nil
}

func multiGridHolds(rows [][]int) bool {
	for _, row := range rows {
		if slices.ContainsFunc(row, func(v int) bool { return v != 0 }) {
			return true
		}
	}
	return false
}

// writeMultiShape puts the minigames and the sorting into the scheme and the
// grids, with the shape's stamp, into the state; every other key of either
// stays as it was.
func writeMultiShape(schemeJSON string, state map[string]json.RawMessage, minigames []MultiGame, order []string, cells [][][]int) ([]byte, []byte, error) {
	scheme := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(schemeJSON), &scheme); err != nil {
		return nil, nil, err
	}
	gamesJSON, err := json.Marshal(minigames)
	if err != nil {
		return nil, nil, err
	}
	scheme["minigames"] = gamesJSON
	delete(scheme, "sorting")
	if len(order) > 0 {
		if scheme["sorting"], err = json.Marshal(order); err != nil {
			return nil, nil, err
		}
	}
	grids := make([]map[string][][]int, len(cells))
	for g := range cells {
		grids[g] = map[string][][]int{"cells": cells[g]}
	}
	if state["games"], err = json.Marshal(grids); err != nil {
		return nil, nil, err
	}
	if state[MultiShapeKey], err = json.Marshal(multiShapeStamp(gamesJSON)); err != nil {
		return nil, nil, err
	}
	schemeOut, err := json.Marshal(scheme)
	if err != nil {
		return nil, nil, err
	}
	stateOut, err := json.Marshal(state)
	return schemeOut, stateOut, err
}

// multiShapeStamp is a short digest of the minigames' JSON: equal shapes
// stamp alike, and a page only compares stamps.
func multiShapeStamp(gamesJSON []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(gamesJSON)
	return fmt.Sprintf("%x", h.Sum64())
}

// FormatMultiGames writes minigames back as the spec ParseMultiGames reads:
// one line a minigame, runs of alike tasks as xN, blocks parted by «|». What
// it writes parses to the same minigames.
func FormatMultiGames(minigames []MultiGame) string {
	lines := make([]string, 0, len(minigames))
	for _, game := range minigames {
		name := game.Name
		if game.Normalized {
			// The arrow as a keyboard types it, which the parser takes too.
			name += " ->0..100"
		}
		var specs []string
		for c := 0; c < len(game.Columns); {
			column := game.Columns[c]
			if c > 0 && column.Block != game.Columns[c-1].Block {
				specs = append(specs, "|")
			}
			run := 1
			for column.Label == "" && c+run < len(game.Columns) && sameMultiTask(column, game.Columns[c+run]) {
				run++
			}
			spec := column.Label + formatMultiDomain(column.Values)
			if run > 1 {
				spec += "x" + strconv.Itoa(run)
			}
			specs = append(specs, spec)
			c += run
		}
		lines = append(lines, name+": "+strings.Join(specs, " "))
	}
	return strings.Join(lines, "\n")
}

func sameMultiTask(a, b MultiColumn) bool {
	return b.Label == "" && a.Block == b.Block && slices.Equal(a.Values, b.Values)
}

// formatMultiDomain writes a run of four or more consecutive values as a
// range, and anything else as a list: {-1,0,1} reads better than {-1-1}.
func formatMultiDomain(values []int) string {
	const shortestRange = 4
	consecutive := len(values) >= shortestRange
	for i := 1; consecutive && i < len(values); i++ {
		consecutive = values[i] == values[i-1]+1
	}
	if consecutive {
		return fmt.Sprintf("{%d-%d}", values[0], values[len(values)-1])
	}
	items := make([]string, len(values))
	for i, v := range values {
		items[i] = strconv.Itoa(v)
	}
	return "{" + strings.Join(items, ",") + "}"
}
