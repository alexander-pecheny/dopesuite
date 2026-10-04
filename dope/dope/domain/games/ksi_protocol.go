package games

import (
	"encoding/json"
	"fmt"

	"dope/dope/domain/structure"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"
)

// ksiFormat is team jeopardy: one flat sheet of themes.
var ksiFormat = Definition{Code: KSI, Label: dopestrings.Default.Games.Ksi.Label(), Title: dopestrings.Default.Host.Games.TypeKsi(),
	Page: "static/si.html", Flat: true, Divisions: true, PlayerOverrides: true, DSL: DSLAccepted,
	Sheets: SheetsKSI, Journal: JournalKSIPatches, Protocol: ksi{}}

// ksi wraps ComputeKSIResults: state is KSIState, the match config
// is the KSI scheme document (its stickers block selects the stickers
// variant). Declined teams keep their slot but stay unplaced.
type ksi struct{}

func (ksi) Code() string { return "ksi" }

func (ksi) Params() []Param { return []Param{{Key: "themes", Config: "themes"}} }

func (ksi) TeamBlob() bool { return false }

func (ksi) Started(state json.RawMessage) bool { return false }

// Metrics: the total and the sum of positive answers.
func (ksi) Metrics(json.RawMessage) []string { return []string{"total", "plus"} }

func (ksi) RatingRosterStateKey() string { return "participants" }

func (ksi) EmptyState(cfg json.RawMessage) (json.RawMessage, error) {
	var conf struct {
		Themes   int             `json:"themes"`
		Stickers json.RawMessage `json:"stickers"`
	}
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &conf); err != nil {
			return nil, fmt.Errorf("ksi config: %w", err)
		}
	}
	if conf.Themes <= 0 {
		conf.Themes = KSIThemeCount
	}
	var stateJSON []byte
	if len(conf.Stickers) > 0 {
		_, stateJSON = KSIStickersEmptyGameJSON("", "", conf.Themes, conf.Stickers)
	} else {
		_, stateJSON = KSIEmptyGameJSON("", "", conf.Themes)
	}
	return stateJSON, nil
}

func (ksi) Seats(stateJSON json.RawMessage) []Seat {
	var state struct {
		Participants json.RawMessage `json:"participants"`
		Declined     map[string]bool `json:"declined"`
	}
	_ = json.Unmarshal(stateJSON, &state)
	participants := ParseKSIParticipants(state.Participants)
	seats := make([]Seat, len(participants))
	for i, p := range participants {
		seats[i] = Seat{Number: int64(p.Number), Name: p.Name, Declined: KSIParticipantDeclined(state.Declined, p)}
	}
	return seats
}

// EnteredSeats: a KSI sheet is one row per team, so a team carries something
// entered when any of its answer cells is filled, when it has chosen a sticker,
// or when the host has marked it as having refused to play on.
func (ksi) EnteredSeats(stateJSON json.RawMessage) []bool {
	var state struct {
		Participants json.RawMessage `json:"participants"`
		Declined     map[string]bool `json:"declined"`
		Stickers     [][]string      `json:"stickers"`
		Themes       []struct {
			Answers [][]string `json:"answers"`
		} `json:"themes"`
	}
	_ = json.Unmarshal(stateJSON, &state)
	participants := ParseKSIParticipants(state.Participants)
	out := make([]bool, len(participants))
	for i, p := range participants {
		out[i] = KSIParticipantDeclined(state.Declined, p)
	}
	markRow := func(row int, filled bool) {
		if filled && row < len(out) {
			out[row] = true
		}
	}
	for _, theme := range state.Themes {
		for row, answers := range theme.Answers {
			markRow(row, anyFilled(answers))
		}
	}
	for row, stickers := range state.Stickers {
		markRow(row, anyFilled(stickers))
	}
	return out
}

func anyFilled(cells []string) bool {
	for _, cell := range cells {
		if cell != "" {
			return true
		}
	}
	return false
}

func (ksi) Score(cfg, stateJSON json.RawMessage) ([]structure.SlotOutcome, error) {
	var state KSIState
	if err := json.Unmarshal(stateJSON, &state); err != nil {
		return nil, fmt.Errorf("ksi state: %w", err)
	}
	ranked, err := ComputeKSIResults(string(cfg), string(stateJSON), store.QuestionValues[:])
	if err != nil {
		return nil, fmt.Errorf("ksi score: %w", err)
	}
	outcomes := make([]structure.SlotOutcome, len(state.Participants))
	for i := range outcomes {
		outcomes[i] = structure.SlotOutcome{Metrics: map[string]float64{}}
	}
	for _, team := range ranked {
		outcomes[team.Index] = structure.SlotOutcome{
			Place: team.Place,
			Metrics: map[string]float64{
				"total": float64(team.Total),
				"plus":  float64(team.Plus),
			},
		}
	}
	return outcomes, nil
}

// A KSI's themes, and its stickers block, which a clear keeps so a stickers
// game stays one. A stored scheme with no themes is read as twenty.
func (ksi) PristineGame(slug, title string, shape Shape) ([]byte, []byte, error) {
	scheme, state := KSIStickersEmptyGameJSON(slug, title, shape.Themes, shape.Stickers)
	return scheme, state, nil
}

func (ksi) ShapeOf(schemeJSON string) Shape {
	var sc struct {
		Themes   int             `json:"themes"`
		Stickers json.RawMessage `json:"stickers"`
	}
	_ = json.Unmarshal([]byte(schemeJSON), &sc)
	if sc.Themes <= 0 {
		sc.Themes = KSIThemeCount
	}
	return Shape{Themes: sc.Themes, Stickers: sc.Stickers}
}

// KSI's theme count rides on the scheme, so the state is resized to it.
func (ksi) FoldRoster(schemeJSON, stateJSON string, teams []RosterTeam, _ map[int]int) ([]byte, []byte, error) {
	scheme, err := ksiRosterScheme(schemeJSON, teams)
	if err != nil {
		return nil, nil, err
	}
	state, err := ksiRosterState(stateJSON, teams, ksiThemeCountFromSchemeJSON(schemeJSON))
	return scheme, state, err
}

func ksiRosterScheme(raw string, teams []RosterTeam) ([]byte, error) {
	obj, err := RawJSONObject(raw)
	if err != nil {
		return nil, err
	}
	themesCount := KSIThemeCount
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
	oldParticipants := ParseKSIParticipants(obj["participants"])
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
		targetThemeCount = KSIThemeCount
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
