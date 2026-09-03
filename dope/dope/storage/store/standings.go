package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"dope/dope/platform/util"
)

// MatchOutcome is one match of a stage as the Structure layer sees it: the
// Protocol scorer's per-slot output, in slot order. Questions is the match's
// base question count (shootouts excluded) — the denominator for share metrics.
type MatchOutcome struct {
	Code      string
	Finished  bool
	Round     int
	Questions int
	Slots     []SlotOutcome
}

// SlotOutcome is one seat's result in a match: who sat there, the effective
// place (scorer's ranking with any host override applied) and the protocol's
// metrics (e.g. "taken", "total"). Place is fractional because shared places
// are (e.g. EK's 1.5); 0 = not placed. The Protocol scorer leaves Participant
// zero — seats are the Structure layer's knowledge, joined in by the caller.
type SlotOutcome struct {
	Participant int64 // 0 = empty seat
	Place       float64
	Metrics     map[string]float64
}

// RankedEntry is one participant's row in a stage's standings. Equal ranks are
// shared on a full tie of the configured order keys; Rank 0 is unplaced — a
// pod's survivor whose places are still being played. Bouts are the codes of
// the matches the row was summed from, when the Kind counts them.
type RankedEntry struct {
	Rank        int
	Participant int64
	Metrics     map[string]float64
	Bouts       []string
}

// LoadMatchOutcomes reads the named matches, in the order given, as the outcomes a
// Ranker sums: two statements for any number of matches.
func LoadMatchOutcomes(ctx context.Context, q Queryer, matchIDs []int64) ([]MatchOutcome, error) {
	if len(matchIDs) == 0 {
		return nil, nil
	}
	args := make([]any, len(matchIDs))
	for i, id := range matchIDs {
		args[i] = id
	}
	in := placeholders(len(matchIDs))
	type matchRow struct {
		id             int64
		status, config string
		outcome        MatchOutcome
	}
	matches, err := CollectRows(ctx, q, `
select m.id, m.code, m.status, m.round, s.config_json
from matches m join stages s on s.id = m.stage_id
where m.id in (`+in+`)`, args, func(rows *sql.Rows) (matchRow, error) {
		var m matchRow
		return m, rows.Scan(&m.id, &m.outcome.Code, &m.status, &m.outcome.Round, &m.config)
	})
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*MatchOutcome, len(matches))
	for i := range matches {
		m := &matches[i]
		m.outcome.Finished = m.status == "finished"
		m.outcome.Questions = ParseStageConfig(m.config).Questions()
		byID[m.id] = &m.outcome
	}
	type slotRow struct {
		matchID     int64
		slot        SlotOutcome
		total, plus float64
		metricsJSON string
	}
	slots, err := CollectRows(ctx, q, `
select ms.match_id, coalesce(ms.participant_id, 0), coalesce(mr.place, 0), coalesce(mr.total, 0), coalesce(mr.plus, 0), coalesce(mr.metrics_json, '{}')
from match_slots ms
left join match_results mr on mr.match_id = ms.match_id and mr.participant_id = ms.participant_id
where ms.match_id in (`+in+`)
order by ms.match_id, ms.slot_index`, args, func(rows *sql.Rows) (slotRow, error) {
		var s slotRow
		return s, rows.Scan(&s.matchID, &s.slot.Participant, &s.slot.Place, &s.total, &s.plus, &s.metricsJSON)
	})
	if err != nil {
		return nil, err
	}
	for _, s := range slots {
		o := byID[s.matchID]
		if o == nil {
			continue
		}
		s.slot.Metrics = map[string]float64{"total": s.total, "plus": s.plus}
		var parsed map[string]any
		if json.Unmarshal([]byte(s.metricsJSON), &parsed) == nil {
			for key, value := range parsed {
				if n, ok := value.(float64); ok {
					s.slot.Metrics[key] = n
				}
			}
		}
		o.Slots = append(o.Slots, s.slot)
	}
	out := make([]MatchOutcome, 0, len(matchIDs))
	for _, id := range matchIDs {
		o := byID[id]
		if o == nil {
			return nil, sql.ErrNoRows
		}
		out = append(out, *o)
	}
	return out, nil
}

// WriteStandings replaces a stage's table with what its Ranker returned (nil
// clears it). The stored rank is the distinct seat order (rank refs must
// resolve uniquely); the Kind's shared display place travels in the metrics,
// and so do the matches the row was summed from, joined for the reseed's
// match column.
func WriteStandings(ctx context.Context, tx *sql.Tx, stageID int64, ranked []RankedEntry) error {
	if _, err := tx.ExecContext(ctx, `delete from stage_standings where stage_id = ?`, stageID); err != nil {
		return err
	}
	for seat, entry := range ranked {
		metrics := map[string]any{"place": float64(entry.Rank)}
		for key, value := range entry.Metrics {
			metrics[key] = value
		}
		if len(entry.Bouts) > 0 {
			metrics["match"] = strings.Join(entry.Bouts, "+")
		}
		if _, err := tx.ExecContext(ctx, `
insert into stage_standings(stage_id, rank, participant_id, metrics_json)
values(?, ?, ?, ?)`, stageID, seat+1, entry.Participant, util.MustJSON(metrics)); err != nil {
			return err
		}
	}
	return nil
}
