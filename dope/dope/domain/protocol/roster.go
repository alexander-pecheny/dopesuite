package protocol

import (
	"encoding/json"
	"strings"

	"dope/dope/domain/games"
	"dope/dope/storage/store"
)

// RosterTeam is a fest roster entry as a flat document lists it: the name and
// city it shows, the Number it is scored under (ADR-0009).
type RosterTeam struct {
	Name   string
	City   string
	Number int64
}

// RosterFolder is a flat Protocol whose scheme and document carry the fest
// roster: ОД's teams array and entries grid, КСИ's participants and answer
// rows. FoldRoster rewrites both for a new roster; entryRemap (old number →
// new) renumbers the cells that key on a Number.
type RosterFolder interface {
	FoldRoster(schemeJSON, stateJSON string, teams []RosterTeam, entryRemap map[int]int) (scheme, state []byte, err error)
}

// FoldRoster folds the roster into a Game's scheme and document through its
// Protocol; ok is false for a Protocol that carries no roster (ЭК, брейн).
func FoldRoster(code, schemeJSON, stateJSON string, teams []RosterTeam, entryRemap map[int]int) (scheme, state []byte, ok bool, err error) {
	p, found := Get(code)
	if !found {
		return nil, nil, false, nil
	}
	folder, ok := p.(RosterFolder)
	if !ok {
		return nil, nil, false, nil
	}
	scheme, state, err = folder.FoldRoster(schemeJSON, stateJSON, teams, entryRemap)
	return scheme, state, true, err
}

func (od) FoldRoster(schemeJSON, stateJSON string, teams []RosterTeam, entryRemap map[int]int) ([]byte, []byte, error) {
	scheme, err := odRosterScheme(schemeJSON, teams)
	if err != nil {
		return nil, nil, err
	}
	state, err := odRosterState(stateJSON, teams, entryRemap)
	return scheme, state, err
}

// КСИ's theme count rides on the scheme, so the state is resized to it.
func (ksi) FoldRoster(schemeJSON, stateJSON string, teams []RosterTeam, _ map[int]int) ([]byte, []byte, error) {
	scheme, err := ksiRosterScheme(schemeJSON, teams)
	if err != nil {
		return nil, nil, err
	}
	state, err := ksiRosterState(stateJSON, teams, ksiThemeCountFromSchemeJSON(schemeJSON))
	return scheme, state, err
}

// ChgkTeamJSON is one team in an OD game's state. OD keys scores by team NUMBER,
// not by position: each cell of state.entries stores the team's Number, and the
// teams array is only a number→name/city lookup. Number is the universal team
// identity (shared with KSI/EK) and is guaranteed present for active teams, so
// re-import reorders never misattribute scores — the entries stay valid as long
// as a team keeps its number (sticky across re-import).
type ChgkTeamJSON struct {
	Name   string `json:"name"`
	City   string `json:"city,omitempty"`
	Number int64  `json:"number,omitempty"`
}

func odRosterScheme(raw string, teams []RosterTeam) ([]byte, error) {
	obj, err := RawJSONObject(raw)
	if err != nil {
		return nil, err
	}
	teamJSON, err := json.Marshal(chgkTeamsFromRoster(teams))
	if err != nil {
		return nil, err
	}
	nTeamsJSON, err := json.Marshal(len(teams))
	if err != nil {
		return nil, err
	}
	obj["teams"] = teamJSON
	obj["nTeams"] = nTeamsJSON
	return json.Marshal(obj)
}

// ApplyRosterToChGKState refreshes an OD game's teams lookup and resizes its
// entries grid for the new roster. Entries hold team NUMBERS as values (the
// universal identity), so a roster reorder needs no per-cell remap — only an
// explicit number reassignment does (entryRemap, supplied by saveFestNumbers,
// nil on plain re-import). See ChgkTeamJSON.
func odRosterState(raw string, teams []RosterTeam, entryRemap map[int]int) ([]byte, error) {
	obj, err := RawJSONObject(raw)
	if err != nil {
		return nil, err
	}
	teamJSON, err := json.Marshal(chgkTeamsFromRoster(teams))
	if err != nil {
		return nil, err
	}
	obj["teams"] = teamJSON

	if rawEntries, ok := obj["entries"]; ok && len(rawEntries) > 0 {
		var entries [][]int
		if err := json.Unmarshal(rawEntries, &entries); err == nil {
			for i := range entries {
				entries[i] = resizeIntSlice(entries[i], len(teams))
				if len(entryRemap) > 0 {
					for j, value := range entries[i] {
						if mapped, ok := entryRemap[value]; ok {
							entries[i][j] = mapped
						}
					}
				}
			}
			entriesJSON, err := json.Marshal(entries)
			if err != nil {
				return nil, err
			}
			obj["entries"] = entriesJSON
		}
	}
	if len(entryRemap) > 0 {
		if rawRounds, ok := obj["shootoutRounds"]; ok && len(rawRounds) > 0 {
			if roundsJSON, err := remapChGKShootoutRounds(rawRounds, entryRemap); err == nil {
				obj["shootoutRounds"] = roundsJSON
			}
		}
	}
	delete(obj, "answers")
	delete(obj, "finished")
	return json.Marshal(obj)
}

func remapChGKShootoutRounds(raw json.RawMessage, entryRemap map[int]int) (json.RawMessage, error) {
	var rounds []chgkShootoutRoundJSON
	if err := json.Unmarshal(raw, &rounds); err != nil {
		return nil, err
	}
	for roundIndex := range rounds {
		for teamIndex, number := range rounds[roundIndex].Teams {
			if mapped, ok := entryRemap[number]; ok {
				rounds[roundIndex].Teams[teamIndex] = mapped
			}
		}
		for questionIndex := range rounds[roundIndex].Entries {
			for slot, number := range rounds[roundIndex].Entries[questionIndex] {
				if mapped, ok := entryRemap[number]; ok {
					rounds[roundIndex].Entries[questionIndex][slot] = mapped
				}
			}
		}
	}
	return json.Marshal(rounds)
}

func ksiRosterScheme(raw string, teams []RosterTeam) ([]byte, error) {
	obj, err := RawJSONObject(raw)
	if err != nil {
		return nil, err
	}
	themesCount := games.KSIThemeCount
	if rawThemes, ok := obj["themes"]; ok && len(rawThemes) > 0 {
		var configured int
		if err := json.Unmarshal(rawThemes, &configured); err == nil && configured > 0 {
			themesCount = configured
		}
	}
	participantsJSON, err := json.Marshal(teamParticipantsFromRoster(teams))
	if err != nil {
		return nil, err
	}
	gameTypeJSON, err := json.Marshal("ksi")
	if err != nil {
		return nil, err
	}
	themesJSON, err := json.Marshal(themesCount)
	if err != nil {
		return nil, err
	}
	obj["gameType"] = gameTypeJSON
	obj["participants"] = participantsJSON
	obj["themes"] = themesJSON
	return json.Marshal(obj)
}

func ksiRosterState(raw string, teams []RosterTeam, targetThemeCount int) ([]byte, error) {
	obj, err := RawJSONObject(raw)
	if err != nil {
		return nil, err
	}
	// Capture the pre-import participant order before overwriting it, so the
	// answer grid (keyed by row position) can be remapped to follow each team
	// across roster reorders/additions/removals instead of staying at its old
	// index. Read tolerantly: new states store [{number,name}], legacy states a
	// bare name array (matched by name for that one transition).
	oldParticipants := games.ParseKSIParticipants(obj["participants"])
	participants := teamParticipantsFromRoster(teams)
	participantsJSON, err := json.Marshal(participants)
	if err != nil {
		return nil, err
	}
	obj["participants"] = participantsJSON

	var themes []map[string]json.RawMessage
	if rawThemes, ok := obj["themes"]; ok && len(rawThemes) > 0 {
		_ = json.Unmarshal(rawThemes, &themes)
	}
	if targetThemeCount <= 0 {
		targetThemeCount = len(themes)
	}
	if targetThemeCount <= 0 {
		targetThemeCount = games.KSIThemeCount
	}
	if len(themes) > targetThemeCount {
		themes = themes[:targetThemeCount]
	}
	for len(themes) < targetThemeCount {
		themes = append(themes, map[string]json.RawMessage{})
	}
	for i := range themes {
		if themes[i] == nil {
			themes[i] = map[string]json.RawMessage{}
		}
		var answers [][]string
		if rawAnswers, ok := themes[i]["answers"]; ok && len(rawAnswers) > 0 {
			_ = json.Unmarshal(rawAnswers, &answers)
		}
		answers = RemapAnswerMatrix(answers, oldParticipants, participants, len(store.QuestionValues))
		answersJSON, err := json.Marshal(answers)
		if err != nil {
			return nil, err
		}
		themes[i]["answers"] = answersJSON
	}
	themesJSON, err := json.Marshal(themes)
	if err != nil {
		return nil, err
	}
	obj["themes"] = themesJSON
	return json.Marshal(obj)
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

func chgkTeamsFromRoster(teams []RosterTeam) []ChgkTeamJSON {
	out := make([]ChgkTeamJSON, 0, len(teams))
	for _, team := range teams {
		out = append(out, ChgkTeamJSON{Name: team.Name, City: team.City, Number: team.Number})
	}
	return out
}

func teamParticipantsFromRoster(teams []RosterTeam) []games.KSIParticipant {
	out := make([]games.KSIParticipant, 0, len(teams))
	for _, team := range teams {
		out = append(out, games.KSIParticipant{Number: int(team.Number), Name: team.Name})
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
// (legacy state captured before numbers were stored). New teams get an empty
// row; teams that dropped out lose their row. Each old row is claimed at most
// once. With no old participants at all, a plain positional resize is used.
func RemapAnswerMatrix(values [][]string, oldParts, newParts []games.KSIParticipant, cols int) [][]string {
	if len(oldParts) == 0 {
		return resizeStringMatrix(values, len(newParts), cols)
	}
	consumed := make([]bool, len(oldParts))
	claim := func(match func(games.KSIParticipant) bool) int {
		for i, p := range oldParts {
			if !consumed[i] && match(p) {
				consumed[i] = true
				return i
			}
		}
		return -1
	}
	out := make([][]string, len(newParts))
	for j, p := range newParts {
		idx := -1
		if p.Number > 0 {
			num := p.Number
			idx = claim(func(o games.KSIParticipant) bool { return o.Number == num })
		}
		if idx < 0 && p.Name != "" {
			name := p.Name
			idx = claim(func(o games.KSIParticipant) bool { return o.Name == name })
		}
		var srcRow []string
		if idx >= 0 && idx < len(values) {
			srcRow = values[idx]
		}
		out[j] = resizeStringSlice(srcRow, cols)
	}
	return out
}

type chgkShootoutRoundJSON struct {
	Teams     []int      `json:"teams"`
	Entries   [][]int    `json:"entries,omitempty"`
	Completed []bool     `json:"completed,omitempty"`
	Answers   [][]string `json:"answers"`
}

func ksiThemeCountFromSchemeJSON(raw string) int {
	obj, err := RawJSONObject(raw)
	if err != nil {
		return 0
	}
	if rawThemes, ok := obj["themes"]; ok && len(rawThemes) > 0 {
		var themesCount int
		if err := json.Unmarshal(rawThemes, &themesCount); err == nil && themesCount > 0 {
			return themesCount
		}
	}
	return 0
}

func resizeStringMatrix(values [][]string, rows, cols int) [][]string {
	if len(values) > rows {
		values = values[:rows]
	}
	out := make([][]string, rows)
	for row := 0; row < rows; row++ {
		if row < len(values) {
			out[row] = resizeStringSlice(values[row], cols)
		} else {
			out[row] = make([]string, cols)
		}
	}
	return out
}

func resizeStringSlice(values []string, size int) []string {
	if len(values) > size {
		return values[:size]
	}
	out := append([]string(nil), values...)
	for len(out) < size {
		out = append(out, "")
	}
	return out
}
