package games

import (
	"encoding/json"
	"strings"
)

// RosterTeam is a fest roster entry as a flat document lists it: the name and
// city it shows, the Number it is scored under (ADR-0009).
type RosterTeam struct {
	Name   string
	City   string
	Number int64
	// Flags are the team's Divisions by short name (ADR-0020). They ride with
	// the name and the city: the document carries them so the page can offer a
	// Division without a second fetch.
	Flags []string
}

// RosterFolder is a flat Protocol whose scheme and document carry the fest
// roster: OD's teams array and entries grid, KSI's participants and answer
// rows. FoldRoster rewrites both for a new roster; entryRemap (old number →
// new) renumbers the cells that key on a Number.
type RosterFolder interface {
	FoldRoster(schemeJSON, stateJSON string, teams []RosterTeam, entryRemap map[int]int) (scheme, state []byte, err error)
}

func RawJSONObject(raw string) (map[string]json.RawMessage, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return map[string]json.RawMessage{}, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		obj = map[string]json.RawMessage{}
	}
	return obj, nil
}

func teamParticipantsFromRoster(teams []RosterTeam) []KSIParticipant {
	out := make([]KSIParticipant, 0, len(teams))
	for _, team := range teams {
		out = append(out, KSIParticipant{Number: int(team.Number), Name: team.Name, City: team.City, Flags: team.Flags})
	}
	return out
}

func resizeIntSlice(values []int, size int) []int {
	if len(values) > size {
		return values[:size]
	}
	out := append([]int(nil), values...)
	for len(out) < size {
		out = append(out, 0)
	}
	return out
}

// RemapAnswerMatrix rebuilds a KSI answer grid for a new participant order,
// moving each old row to wherever its team now sits so scores follow their team
// across roster reorders, additions, and removals. Teams are matched by NUMBER
// (the universal, unique identity) — so two teams sharing a name keep distinct
// scores — falling back to name only when the old participant has no number
// (legacy state captured before numbers were stored). A Multi guest team's
// number, below zero, matches the same way. New teams get an empty
// row; teams that dropped out lose their row. Each old row is claimed at most
// once. With no old participants at all, a plain positional resize is used.
func RemapAnswerMatrix[T any](values [][]T, oldParts, newParts []KSIParticipant, cols int) [][]T {
	if len(oldParts) == 0 {
		return resizeMatrix(values, len(newParts), cols)
	}
	consumed := make([]bool, len(oldParts))
	claim := func(match func(KSIParticipant) bool) int {
		for i, p := range oldParts {
			if !consumed[i] && match(p) {
				consumed[i] = true
				return i
			}
		}
		return -1
	}
	out := make([][]T, len(newParts))
	for j, p := range newParts {
		idx := -1
		if p.Number != 0 {
			num := p.Number
			idx = claim(func(o KSIParticipant) bool { return o.Number == num })
		}
		if idx < 0 && p.Name != "" {
			name := p.Name
			idx = claim(func(o KSIParticipant) bool { return o.Name == name })
		}
		var srcRow []T
		if idx >= 0 && idx < len(values) {
			srcRow = values[idx]
		}
		out[j] = resizeRow(srcRow, cols)
	}
	return out
}

func resizeMatrix[T any](values [][]T, rows, cols int) [][]T {
	if len(values) > rows {
		values = values[:rows]
	}
	out := make([][]T, rows)
	for row := 0; row < rows; row++ {
		if row < len(values) {
			out[row] = resizeRow(values[row], cols)
		} else {
			out[row] = make([]T, cols)
		}
	}
	return out
}

func resizeRow[T any](values []T, size int) []T {
	if len(values) > size {
		return values[:size]
	}
	out := append([]T(nil), values...)
	var zero T
	for len(out) < size {
		out = append(out, zero)
	}
	return out
}
