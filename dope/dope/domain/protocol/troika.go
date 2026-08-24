package protocol

import (
	"encoding/json"
	"fmt"

	"dope/dope/domain/games"
	"dope/dope/domain/structure"
)

func init() { Register(troika{}) }

// troika wraps games.ComputeTroikaResults: state is games.TroikaState, and the
// бой's shape — how many темы, what each is worth — comes from its stage
// config at build time and is recorded in the document.
type troika struct{}

func (troika) Code() string { return "troika" }

func (troika) Params() []Param {
	return []Param{
		{Key: "themes", Config: "themes", Default: games.TroikaThemeCount},
		{Key: "theme_values", Config: "themeValues", List: true},
	}
}

func (troika) TeamBlob() bool { return false }

func (troika) Started(state json.RawMessage) bool { return games.TroikaStateStarted(string(state)) }

// Metrics: игровые очки и правильные ответы без учёта номинала. A группа sums
// очки into забито/пропущено with `metric: total`, and the регламент's
// рейтинговый балл is a scoring rule over them.
func (troika) Metrics(json.RawMessage) []string { return []string{"total", "correct"} }

func (troika) EmptyState(cfg json.RawMessage) (json.RawMessage, error) {
	var conf struct {
		Themes      int   `json:"themes"`
		ThemeValues []int `json:"themeValues"`
	}
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &conf); err != nil {
			return nil, fmt.Errorf("troika config: %w", err)
		}
	}
	return games.TroikaEmptyStateJSON(games.TroikaThemeValues(conf.Themes, conf.ThemeValues)), nil
}

func (troika) Score(cfg, stateJSON json.RawMessage) ([]structure.SlotOutcome, error) {
	results, err := games.ComputeTroikaResults(string(stateJSON))
	if err != nil {
		return nil, fmt.Errorf("troika score: %w", err)
	}
	outcomes := make([]structure.SlotOutcome, len(results))
	for i, side := range results {
		outcomes[i] = structure.SlotOutcome{
			Place: side.Place,
			Metrics: map[string]float64{
				"total":   float64(side.Total),
				"correct": float64(side.Correct),
			},
		}
	}
	return outcomes, nil
}
