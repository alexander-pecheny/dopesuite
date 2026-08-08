package protocol

import (
	"context"
	"database/sql"
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

// Metrics: сумма, сумма положительных ответов, и счётчики взятых по номиналам —
// СИ ранжирует по ним, когда суммы равны.
func (si) Metrics() []string {
	return []string{"total", "plus", "taken50", "taken40", "taken30", "taken20", "taken10"}
}

// EmptyState is the empty blob: a бой's seats come from its Slots and its marks
// arrive as edits, so a fresh бой's document holds nothing at all.
func (si) EmptyState(cfg json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage("{}"), nil
}

// WriteResultsTx scores a бой the way ЭК's scorer reads the same blob, then
// replaces ЭК's host-entered places with the ones сумма dictates. It satisfies
// scoring.LegacyResultWriter, so this is the only path a си бой is scored by.
func (si) WriteResultsTx(ctx context.Context, tx *sql.Tx, match store.DBMatchState) error {
	view := store.BuildView(match.State)
	places := placesBySum(view.Participants)
	for index, player := range view.Participants {
		if index >= len(match.ParticipantIDs) || match.ParticipantIDs[index] == 0 {
			continue
		}
		place := places[index]
		if pin := match.Blob.Pin(match.ParticipantIDs[index]); pin != nil {
			place = *pin
		}
		metrics := map[string]any{"total": player.Total, "plus": player.Plus}
		for i, value := range store.QuestionValues {
			metrics[fmt.Sprintf("taken%d", value)] = player.CorrectCounts[i]
		}
		if _, err := tx.ExecContext(ctx, `
insert into match_results(match_id, participant_id, place, total, plus, tiebreak, metrics_json)
values(?, ?, ?, ?, ?, 0, ?)
on conflict(match_id, participant_id) do update set
  place = excluded.place,
  total = excluded.total,
  plus = excluded.plus,
  metrics_json = excluded.metrics_json`,
			match.MatchID, match.ParticipantIDs[index], place,
			player.Total, player.Plus, mustJSON(metrics)); err != nil {
			return err
		}
	}
	return nil
}

// Score is the Structure-facing scorer, used where a бой is scored outside a
// transaction (tests, exports). It reads the same blob-projected state.
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
			"total": float64(player.Total),
			"plus":  float64(player.Plus),
		}
		for k, value := range store.QuestionValues {
			metrics[fmt.Sprintf("taken%d", value)] = float64(player.CorrectCounts[k])
		}
		outcomes[i] = structure.SlotOutcome{Place: places[i], Metrics: metrics}
	}
	return outcomes, nil
}

// placesBySum ranks a бой by сумма alone, sharing a place on equal sums — two
// players who both took 110 finish 1.5, not 1 and 2. ЭК would split such a tie
// with a перестрелка and КСИ by Σ+, but личная СИ pays очки by place, so a
// split here would invent a difference the бой did not produce. Σ+ and the
// per-value counts are still emitted; the group table sorts on them.
func placesBySum(players []store.ParticipantView) []float64 {
	order := make([]int, len(players))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return players[order[a]].Total > players[order[b]].Total
	})
	places := make([]float64, len(players))
	for start := 0; start < len(order); {
		end := start + 1
		for end < len(order) && players[order[end]].Total == players[order[start]].Total {
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

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}
