package games

import (
	"encoding/json"
	"fmt"

	"dope/dope/domain/structure"
	dopestrings "dope/i18nstrings"
)

// odFormat is ChGK: one flat sheet of tours, the teams taking questions by
// number.
var odFormat = Definition{Code: OD, Label: dopestrings.Default.Games.Od.Label(), Title: dopestrings.Default.Host.Games.TypeOd(),
	Page: "static/od.html", Flat: true, Divisions: true, DSL: DSLAccepted, ToursSeed: true,
	Results: odResults, Sheets: SheetsODRating, Journal: JournalODPatches, Protocol: od{}}

func odResults(schemeJSON, stateJSON string) (any, error) {
	return ComputeODResults(schemeJSON, stateJSON)
}

// od is OD's Protocol: OD's sheet, seated by the fest teams' numbers and
// carrying the fest roster (FoldRoster).
type od struct{ odSheet }

// odSheet is the part of the Protocol an OD document plays by, which OD and
// the friendship cup share (kd embeds it): state is ODState, ranked by
// ComputeODResults. The match config is the OD scheme document (its tourComp
// drives the tour split). Teams tied on total share a place, matching the
// results page's tie-grouped labels. Who the teams are — fest teams or a
// cup's tables — is each format's own, so seating, the roster and the
// pristine Game are not here.
type odSheet struct{}

func (od) Code() string { return "od" }

func (odSheet) Params() []Param { return []Param{{Key: "tour_comp", Config: "tourComp", List: true}} }

func (odSheet) TeamBlob() bool { return false }

func (odSheet) Started(state json.RawMessage) bool { return false }

// Metrics: questions taken and the Buchholz rating.
func (odSheet) Metrics(json.RawMessage) []string { return []string{"total", "rating"} }

// RatingRosterStateKey keeps the teams fixed: a PATCH may not rename or
// renumber them (a cup's tables either), only the answers change.
func (odSheet) RatingRosterStateKey() string { return "teams" }

func (odSheet) EmptyState(cfg json.RawMessage) (json.RawMessage, error) {
	_, stateJSON := ODEmptyGameJSON("", "", ParseTourComp(string(cfg)))
	return stateJSON, nil
}

func (od) Seats(stateJSON json.RawMessage) []Seat {
	var state ODState
	_ = json.Unmarshal(stateJSON, &state)
	seats := make([]Seat, len(state.Teams))
	for i, team := range state.Teams {
		seats[i] = Seat{Number: team.Number, Name: team.Name, City: team.City}
	}
	return seats
}

// EnteredSeats: an OD sheet records, per question, the numbers of the teams
// that took it, so a team carries something entered as soon as its number is
// written anywhere — on a question of the tours, or in a shootout round.
func (od) EnteredSeats(stateJSON json.RawMessage) []bool {
	var state struct {
		Teams    []ChgkTeamJSON          `json:"teams"`
		Entries  [][]int64               `json:"entries"`
		Shootout []chgkShootoutRoundJSON `json:"shootoutRounds"`
	}
	_ = json.Unmarshal(stateJSON, &state)
	written := make(map[int64]bool)
	for _, question := range state.Entries {
		for _, number := range question {
			written[number] = true
		}
	}
	for _, round := range state.Shootout {
		for _, number := range round.Teams {
			written[int64(number)] = true
		}
		for _, question := range round.Entries {
			for _, number := range question {
				written[int64(number)] = true
			}
		}
	}
	out := make([]bool, len(state.Teams))
	for i, team := range state.Teams {
		out[i] = team.Number > 0 && written[team.Number]
	}
	return out
}

func (odSheet) Score(cfg, stateJSON json.RawMessage) ([]structure.SlotOutcome, error) {
	results, err := ComputeODResults(string(cfg), string(stateJSON))
	if err != nil {
		return nil, fmt.Errorf("od score: %w", err)
	}
	outcomes := make([]structure.SlotOutcome, resultTeamCount(results))
	place, placed := 0.0, 0
	for rank, team := range results.Teams {
		if rank == 0 || team.Total != results.Teams[rank-1].Total {
			place = float64(placed + 1)
		}
		placed++
		outcome := structure.SlotOutcome{
			Metrics: map[string]float64{
				"total":  float64(team.Total),
				"rating": float64(team.Rating),
			},
		}
		if team.Place != "" {
			outcome.Place = place
		}
		outcomes[team.Index] = outcome
	}
	return outcomes, nil
}

func resultTeamCount(results ODResults) int {
	max := 0
	for _, team := range results.Teams {
		if team.Index+1 > max {
			max = team.Index + 1
		}
	}
	return max
}

// An OD Game's tours. A stored scheme with none is read as one tour of
// fifteen, which is what a clear always rebuilt it with.
func (od) PristineGame(slug, title string, shape Shape) ([]byte, []byte, error) {
	scheme, state := ODEmptyGameJSON(slug, title, shape.Tours)
	return scheme, state, nil
}

// odDefaultTourQuestions is the one tour an OD game has when its scheme
// records none.
const odDefaultTourQuestions = 15

func (od) ShapeOf(schemeJSON string) Shape {
	tours := ParseTourComp(schemeJSON)
	if len(tours) == 0 {
		tours = []int{odDefaultTourQuestions}
	}
	return Shape{Tours: tours}
}

func (od) FoldRoster(schemeJSON, stateJSON string, teams []RosterTeam, entryRemap map[int]int) ([]byte, []byte, error) {
	scheme, err := odRosterScheme(schemeJSON, teams)
	if err != nil {
		return nil, nil, err
	}
	state, err := odRosterState(stateJSON, teams, entryRemap)
	return scheme, state, err
}

// ChgkTeamJSON is one team in an OD game's state. OD keys scores by team NUMBER,
// not by position: each cell of state.entries stores the team's Number, and the
// teams array is only a number→name/city lookup. Number is the universal team
// identity (shared with KSI/EK) and is guaranteed present for active teams, so
// re-import reorders never misattribute scores — the entries stay valid as long
// as a team keeps its number (sticky across re-import).
type ChgkTeamJSON struct {
	Name   string   `json:"name"`
	City   string   `json:"city,omitempty"`
	Number int64    `json:"number,omitempty"`
	Flags  []string `json:"flags,omitempty"`
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

type chgkShootoutRoundJSON struct {
	Teams     []int      `json:"teams"`
	Entries   [][]int    `json:"entries,omitempty"`
	Completed []bool     `json:"completed,omitempty"`
	Answers   [][]string `json:"answers"`
}

func chgkTeamsFromRoster(teams []RosterTeam) []ChgkTeamJSON {
	out := make([]ChgkTeamJSON, 0, len(teams))
	for _, team := range teams {
		out = append(out, ChgkTeamJSON{Name: team.Name, City: team.City, Number: team.Number, Flags: team.Flags})
	}
	return out
}
