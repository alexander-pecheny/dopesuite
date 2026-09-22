package protocol

import (
	"encoding/json"
	"fmt"

	"dope/dope/domain/games"
	"dope/dope/domain/structure"
)

func init() { Register(hamsa{}) }

// hamsa wraps games.ComputeHamsaResults. A бой's shape — how many темы each
// game round plays, what a вопрос is worth there, whether the Блок allows a
// перестрелка — comes from its stage config at build time and is recorded in
// the document, because what a вопрос paid is a fact about the бой that played
// it.
type hamsa struct{}

func (hamsa) Code() string { return games.Hamsa }

func (hamsa) Params() []Param {
	return []Param{
		{Key: "game_rounds", Config: "gameRounds", List: true},
		{Key: "multipliers", Config: "multipliers", List: true},
		{Key: "values", Config: "values", List: true},
		{Key: "shootout", Config: "shootout", Bool: true},
	}
}

func (hamsa) TeamBlob() bool { return false }

// A тема records the player who sat for it, so a бой's seats carry their rosters.
func (hamsa) SeatsPlayers() bool { return true }

func (hamsa) Started(state json.RawMessage) bool { return games.HamsaStateStarted(string(state)) }

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

// Metrics: the score, Σ+, the перестрелка, the first places a бой hands out,
// and how many вопросы each номинал was taken and lost at — the columns the
// regulations' tiebreaks and the statistics tab are written in.
func (hamsa) Metrics(cfg json.RawMessage) []string {
	conf, err := readHamsaConfig(cfg)
	if err != nil {
		conf = hamsaConfig{}
	}
	names := []string{"total", "plus", "shootoutTotal", "first"}
	for _, value := range games.HamsaBaseValues(games.HamsaGameRounds(conf.GameRounds, conf.Multipliers, conf.Values)) {
		names = append(names, fmt.Sprintf("correct_%d", value), fmt.Sprintf("wrong_%d", value))
	}
	return names
}

// EmptyState writes the effective per-тема номиналы into the document and
// nothing else: a бой's seats come from its Slots and its marks arrive as
// edits.
func (hamsa) EmptyState(cfg json.RawMessage) (json.RawMessage, error) {
	conf, err := readHamsaConfig(cfg)
	if err != nil {
		return nil, err
	}
	return games.HamsaEmptyStateJSON(games.HamsaGameRounds(conf.GameRounds, conf.Multipliers, conf.Values)), nil
}

func (hamsa) Score(cfg, stateJSON json.RawMessage) ([]structure.SlotOutcome, error) {
	return hamsa{}.ScoreSeated(cfg, stateJSON, nil)
}

// ScoreSeated scores the бой's seats, in slot order: the document is keyed by
// Participant, so the scorer hands over who is sitting there.
func (hamsa) ScoreSeated(_ json.RawMessage, stateJSON json.RawMessage, seats []int64) ([]structure.SlotOutcome, error) {
	results, err := games.ComputeHamsaResults(string(stateJSON), seats)
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
