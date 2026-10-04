package games

import (
	"encoding/json"
	"fmt"
	"strconv"

	"dope/dope/domain/structure"
	dopestrings "dope/i18nstrings"
)

// hamsaFormat plays a bracket of four-seat bouts and boots the bracket
// payload, as Troika does: the page fetches its matches and draws them its
// own way.
var hamsaFormat = Definition{Code: Hamsa, Label: dopestrings.Default.Games.Hamsa.Label(), Title: dopestrings.Default.Host.Games.TypeHamsa(),
	Page: "static/hamsa.html", HandRoster: true, DSL: DSLAccepted,
	DefaultDSL: HamsaDefaultDSL, Sheets: SheetsHamsa, Journal: JournalEvents, Protocol: hamsa{}}

// hamsa wraps ComputeHamsaResults. A bout's shape — how many themes each
// game round plays, what a question is worth there, whether the Block allows a
// shootout — comes from its stage config at build time and is recorded in the
// document, because what a question paid is a fact about the bout that played
// it.
type hamsa struct{}

func (hamsa) Code() string { return Hamsa }

func (hamsa) Params() []Param {
	return []Param{
		{Key: "game_rounds", Config: "gameRounds", List: true},
		{Key: "multipliers", Config: "multipliers", List: true},
		{Key: "values", Config: "values", List: true},
		{Key: "shootout", Config: "shootout", Bool: true},
	}
}

func (hamsa) TeamBlob() bool { return false }

// A theme records the player who sat for it, so a bout's seats carry rosters.
func (hamsa) SeatsPlayers() bool { return true }

// The lot among teams that share a place is drawn once the bout is over,
// so it is the one entry a finished bout takes: ["participants", <id>, "lot"].
func (hamsa) EditableWhenFinished(path []json.RawMessage) bool {
	if len(path) != 3 {
		return false
	}
	var head, tail string
	return json.Unmarshal(path[0], &head) == nil && head == "participants" &&
		json.Unmarshal(path[2], &tail) == nil && tail == "lot"
}

// UsedPlayers: Hamsa's document keys each side by Participant, and a theme
// (the shootout's too) names the player who sat it.
func (hamsa) UsedPlayers(stateJSON json.RawMessage, _ []int64) []UsedPlayer {
	var state HamsaState
	if json.Unmarshal(stateJSON, &state) != nil {
		return nil
	}
	var out []UsedPlayer
	for key, side := range state.Participants {
		if side == nil {
			continue
		}
		team, _ := strconv.ParseInt(key, 10, 64)
		for _, theme := range append(append([]HamsaTheme(nil), side.Themes...), side.Shootout...) {
			out = append(out, UsedPlayer{Team: team, Player: theme.Player})
		}
	}
	return out
}

func (hamsa) Started(state json.RawMessage) bool { return HamsaStateStarted(string(state)) }

// hamsaConfig is the stage config the compiler writes from the DSL params.
type hamsaConfig struct {
	GameRounds  []int `json:"gameRounds"`
	Multipliers []int `json:"multipliers"`
	Values      []int `json:"values"`
	Shootout    bool  `json:"shootout"`
}

func readHamsaConfig(cfg json.RawMessage) (hamsaConfig, error) {
	var conf hamsaConfig
	if len(cfg) == 0 {
		return conf, nil
	}
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return hamsaConfig{}, fmt.Errorf("hamsa config: %w", err)
	}
	return conf, nil
}

// Metrics: the score, the plus column, the shootout, the first places a bout
// hands out, and how many questions each value was taken and lost at — the
// columns the regulations' tiebreaks and the statistics tab are written in.
// advance_place is the place a team goes forward from: a place nobody shares,
// or the one the host's lot gives it within a shared place, and 0 while
// that is not drawn. The resolver reads it before the place itself.
func (hamsa) Metrics(cfg json.RawMessage) []string {
	conf, err := readHamsaConfig(cfg)
	if err != nil {
		conf = hamsaConfig{}
	}
	names := []string{"total", "plus", "shootoutTotal", "first", "advance_place"}
	for _, value := range HamsaBaseValues(HamsaGameRounds(conf.GameRounds, conf.Multipliers, conf.Values)) {
		names = append(names, fmt.Sprintf("correct_%d", value), fmt.Sprintf("wrong_%d", value))
	}
	return names
}

// EmptyState writes the effective per-theme values into the document and
// nothing else: a bout's seats come from its Slots and its marks arrive as
// edits.
func (hamsa) EmptyState(cfg json.RawMessage) (json.RawMessage, error) {
	conf, err := readHamsaConfig(cfg)
	if err != nil {
		return nil, err
	}
	return HamsaEmptyStateJSON(HamsaGameRounds(conf.GameRounds, conf.Multipliers, conf.Values)), nil
}

func (hamsa) Score(cfg, stateJSON json.RawMessage) ([]structure.SlotOutcome, error) {
	return hamsa{}.ScoreSeated(cfg, stateJSON, nil)
}

// ScoreSeated scores the bout's seats, in slot order: the document is keyed by
// Participant, so the scorer hands over who is sitting there.
func (hamsa) ScoreSeated(_ json.RawMessage, stateJSON json.RawMessage, seats []int64) ([]structure.SlotOutcome, error) {
	results, err := ComputeHamsaResults(string(stateJSON), seats)
	if err != nil {
		return nil, fmt.Errorf("hamsa score: %w", err)
	}
	outcomes := make([]structure.SlotOutcome, len(results))
	for i, result := range results {
		metrics := map[string]float64{
			"total":         float64(result.Total),
			"plus":          float64(result.Plus),
			"shootoutTotal": float64(result.ShootoutTotal),
			"first":         result.First,
			"advance_place": result.Order,
		}
		for value, count := range result.Correct {
			metrics[fmt.Sprintf("correct_%d", value)] = float64(count)
		}
		for value, count := range result.Wrong {
			metrics[fmt.Sprintf("wrong_%d", value)] = float64(count)
		}
		outcomes[i] = structure.SlotOutcome{Participant: result.Participant, Place: result.Place, Metrics: metrics}
	}
	return outcomes, nil
}
