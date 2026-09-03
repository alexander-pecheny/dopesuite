package protocol

import (
	"encoding/json"
	"fmt"
	"sort"

	"dope/dope/domain/structure"
	"dope/dope/storage/store"
)

func init() { Register(si{}) }

// si is individual jeopardy: three or four players at a table over six, eight
// or twelve themes, each theme five questions at 10..50.
//
// Its Match state is the same blob EK uses — participant × theme × five
// answers is the shape of both — which is why a match of individual SI is
// played on the very screen EK is played on, with no renderer of its own.
// Individual SI differs from EK in one thing only: nobody enters places by
// hand. A match ranks by total, and equal totals share a place, because a
// place is what the group pays points for.
type si struct{}

func (si) Code() string { return "si" }

func (si) Params() []Param {
	return []Param{{Key: "themes", Config: "themes"}}
}

func (si) TeamBlob() bool { return true }

func (si) Started(state json.RawMessage) bool { return false }

// Metrics: the total, the sum of positive answers, the shootout and the
// per-nominal taken counts — SI ranks on them when totals tie.
func (si) Metrics(json.RawMessage) []string {
	return []string{"total", "plus", "shootoutTotal", "taken50", "taken40", "taken30", "taken20", "taken10"}
}

// EmptyState is the empty blob: a match's seats come from its Slots and its
// marks arrive as edits, so a fresh match's document holds nothing at all.
func (si) EmptyState(cfg json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage("{}"), nil
}

// Score ranks the blob-projected state by total — nobody enters places by
// hand in individual SI — and reports the same metrics EK's scorer reads.
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

// placesBySum ranks a match by total, then by shootout where totals tie —
// extra material played exactly to break the tie, held outside Σ. A tie the
// shootout did not touch stays shared: two players who both took 110 finish
// 1.5, not 1 and 2, because individual SI pays points by place and a split
// here would invent a difference the match did not produce. Σ+ and the
// per-value counts are still emitted; the group table sorts on them.
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
