package protocol

import (
	"encoding/json"
	"fmt"
	"sort"

	"dope/dope/domain/structure"
	"dope/dope/storage/store"
)

func init() { Register(si{}) }

// si is личная своя игра: three or four players at a table over six, eight or
// twelve themes, each theme five questions at 10..50.
//
// Its Match state is the same blob ЭК uses — «участник × тема × пять ответов»
// is the shape of both — which is why a бой of личная СИ is played on the very
// screen ЭК is played on, with no renderer of its own. Личная СИ differs from
// ЭК in one thing only: nobody enters places by hand. A бой ranks by сумма, and
// equal sums share a place, because место is what the group pays очки for.
type si struct{}

func (si) Code() string { return "si" }

func (si) Params() []Param {
	return []Param{{Key: "themes", Config: "themes"}, {Key: "participants", Config: "participants"}}
}

func (si) TeamBlob() bool { return true }

func (si) Started(state json.RawMessage) bool { return false }

// Metrics: сумма, сумма положительных ответов, перестрелка и счётчики взятых
// по номиналам — СИ ранжирует по ним, когда суммы равны.
func (si) Metrics() []string {
	return []string{"total", "plus", "shootoutTotal", "taken50", "taken40", "taken30", "taken20", "taken10"}
}

// EmptyState is the empty blob: a бой's seats come from its Slots and its marks
// arrive as edits, so a fresh бой's document holds nothing at all.
func (si) EmptyState(cfg json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage("{}"), nil
}

// Score ranks the blob-projected state by сумма — nobody enters places by hand
// in личная СИ — and reports the same metrics ЭК's scorer reads.
func (si) Score(cfg, stateJSON json.RawMessage) ([]structure.SlotOutcome, error) {
	var state store.MatchState
	if err := json.Unmarshal(stateJSON, &state); err != nil {
		return nil, fmt.Errorf("si state: %w", err)
	}
	view := store.BuildView(state)
	places := placesBySum(view.Participants)
	outcomes := make([]structure.SlotOutcome, len(view.Participants))
	for i, player := range view.Participants {
		metrics := map[string]float64{
			"total":         float64(player.Total),
			"plus":          float64(player.Plus),
			"shootoutTotal": float64(player.ShootoutTotal),
		}
		for k, value := range store.QuestionValues {
			metrics[fmt.Sprintf("taken%d", value)] = float64(player.CorrectCounts[k])
		}
		outcomes[i] = structure.SlotOutcome{Place: places[i], Metrics: metrics}
	}
	return outcomes, nil
}

// placesBySum ranks a бой by сумма, then by перестрелка where sums tie — extra
// material played exactly to break the tie, held outside Σ. A tie the
// перестрелка did not touch stays shared: two players who both took 110 finish
// 1.5, not 1 and 2, because личная СИ pays очки by place and a split here would
// invent a difference the бой did not produce. Σ+ and the per-value counts are
// still emitted; the group table sorts on them.
func placesBySum(players []store.ParticipantView) []float64 {
	order := make([]int, len(players))
	for i := range order {
		order[i] = i
	}
	rank := func(p store.ParticipantView) [2]int { return [2]int{p.Total, p.ShootoutTotal} }
	sort.SliceStable(order, func(a, b int) bool {
		ra, rb := rank(players[order[a]]), rank(players[order[b]])
		if ra[0] != rb[0] {
			return ra[0] > rb[0]
		}
		return ra[1] > rb[1]
	})
	places := make([]float64, len(players))
	for start := 0; start < len(order); {
		end := start + 1
		for end < len(order) && rank(players[order[end]]) == rank(players[order[start]]) {
			end++
		}
		place := float64(start+end+1) / 2
		for i := start; i < end; i++ {
			places[order[i]] = place
		}
		start = end
	}
	return places
}
