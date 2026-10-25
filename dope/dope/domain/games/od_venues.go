package games

import (
	"encoding/json"
	"slices"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// ODSheet is one venue's part of a synch fest's OD (ADR-0033): for each
// question the venue numbers of its teams that took it, in slots like a whole
// OD's, and which questions the venue has completed.
type ODSheet struct {
	Entries   [][]int64 `json:"entries"`
	Completed []bool    `json:"completed"`
}

// FlattenVenues folds the venue sheets into the document's own entries and
// completion, keyed by the teams' fest numbers, so the scoring reads one sheet:
// a team took a question its venue completed with its number entered, and a
// question is completed once any venue completed it.
func (s *ODState) FlattenVenues() {
	if len(s.Venues) == 0 {
		return
	}
	n := 0
	for _, sheet := range s.Venues {
		n = max(n, len(sheet.Completed))
	}
	entries := make([][]int64, n)
	completed := make([]bool, n)
	for q := range entries {
		entries[q] = []int64{}
	}
	for _, team := range s.Teams {
		sheet, ok := s.Venues[idstr.Format(team.Venue)]
		if !ok || team.VenueNumber <= 0 {
			continue
		}
		for q := range min(len(sheet.Entries), len(sheet.Completed)) {
			if sheet.Completed[q] && slices.Contains(sheet.Entries[q], team.VenueNumber) {
				entries[q] = append(entries[q], team.Number)
			}
		}
	}
	for _, sheet := range s.Venues {
		for q, done := range sheet.Completed {
			completed[q] = completed[q] || done
		}
	}
	s.Entries, s.Completed = entries, completed
}

// foldVenueSheets keeps a sheet for every venue the teams play at, its slots
// as many as the venue's teams and a completion per question; a venue that
// lost its teams keeps its sheet for when they come back.
func foldVenueSheets(obj map[string]json.RawMessage, teams []RosterTeam, questions int) error {
	counts := map[string]int{}
	for _, team := range teams {
		if team.Venue > 0 {
			counts[idstr.Format(team.Venue)]++
		}
	}
	if len(counts) == 0 {
		return nil
	}
	sheets := map[string]ODSheet{}
	if raw, ok := obj["venues"]; ok {
		if err := json.Unmarshal(raw, &sheets); err != nil {
			return err
		}
	}
	for venue, count := range counts {
		sheet := sheets[venue]
		sheet.Entries = resizeSheet(sheet.Entries, questions, count)
		sheet.Completed = append(sheet.Completed, make([]bool, max(0, questions-len(sheet.Completed)))...)[:questions]
		sheets[venue] = sheet
	}
	raw, err := json.Marshal(sheets)
	obj["venues"] = raw
	return err
}

// resizeSheet gives a sheet a column per question and slots for count teams.
func resizeSheet(entries [][]int64, questions, count int) [][]int64 {
	out := make([][]int64, questions)
	for q := range out {
		var column []int64
		if q < len(entries) {
			column = entries[q]
		}
		out[q] = append(column[:min(len(column), count):min(len(column), count)], make([]int64, max(0, count-len(column)))...)
	}
	return out
}

// RemapVenueSheet renumbers the cells of one venue's sheet after the venue
// renumbered its teams (old number → new).
func (od) RemapVenueSheet(stateJSON []byte, venueID int64, remap map[int]int) ([]byte, error) {
	obj, err := RawJSONObject(string(stateJSON))
	if err != nil || len(remap) == 0 {
		return stateJSON, err
	}
	sheets := map[string]ODSheet{}
	if raw, ok := obj["venues"]; ok {
		if err := json.Unmarshal(raw, &sheets); err != nil {
			return nil, err
		}
	}
	key := idstr.Format(venueID)
	sheet, ok := sheets[key]
	if !ok {
		return stateJSON, nil
	}
	for q := range sheet.Entries {
		for slot, number := range sheet.Entries[q] {
			if mapped, ok := remap[int(number)]; ok {
				sheet.Entries[q][slot] = int64(mapped)
			}
		}
	}
	sheets[key] = sheet
	if obj["venues"], err = json.Marshal(sheets); err != nil {
		return nil, err
	}
	return json.Marshal(obj)
}

// enteredOnVenues is EnteredSeats for a document with venue sheets: a team
// carries something once its venue number is written on its venue's sheet.
func enteredOnVenues(stateJSON json.RawMessage) ([]bool, bool) {
	var state ODState
	if json.Unmarshal(stateJSON, &state) != nil || len(state.Venues) == 0 {
		return nil, false
	}
	out := make([]bool, len(state.Teams))
	for i, team := range state.Teams {
		sheet := state.Venues[idstr.Format(team.Venue)]
		for _, column := range sheet.Entries {
			if team.VenueNumber > 0 && slices.Contains(column, team.VenueNumber) {
				out[i] = true
			}
		}
	}
	return out, true
}
