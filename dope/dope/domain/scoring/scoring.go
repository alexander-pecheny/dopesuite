// Package scoring materialises a match's Protocol outcome into match_results —
// the only rows the Structure layer reads (docs/unified-model.md §2). Places
// are the scorer's, unless the host pinned a place_override (ADR-0001:
// auto-places with manual override).
package scoring

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"dope/dope/domain/protocol"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
)

// LegacyResultWriter is implemented by protocols whose match_results rows
// predate the generic SlotOutcome path and must keep their exact legacy shape
// (EK's metrics_json with correctCounts arrays). Everything else goes through
// the generic writer below.
type LegacyResultWriter interface {
	WriteResultsTx(ctx context.Context, tx *sql.Tx, match store.DBMatchState) error
}

// RecalculateMatchResultsTx scores a match through its game's registered
// Protocol and upserts match_results for every occupied slot, honouring
// place_override.
func RecalculateMatchResultsTx(ctx context.Context, tx *sql.Tx, match store.DBMatchState) error {
	var protocolCode string
	if err := tx.QueryRowContext(ctx,
		`select game_type from games where id = ?`, match.GameID).Scan(&protocolCode); err != nil {
		return err
	}
	p, ok := protocol.Get(protocolCode)
	if !ok {
		return fmt.Errorf("scoring: no protocol %q", protocolCode)
	}
	if legacy, ok := p.(LegacyResultWriter); ok {
		return legacy.WriteResultsTx(ctx, tx, match)
	}
	stateJSON, err := json.Marshal(match.State)
	if err != nil {
		return err
	}
	outcomes, err := p.Score(nil, stateJSON)
	if err != nil {
		return err
	}
	for index, outcome := range outcomes {
		if index >= len(match.TeamIDs) || match.TeamIDs[index] == 0 {
			continue
		}
		place, err := effectivePlace(ctx, tx, match.MatchID, match.TeamIDs[index], outcome.Place)
		if err != nil {
			return err
		}
		metrics := map[string]any{}
		for key, value := range outcome.Metrics {
			metrics[key] = value
		}
		if _, err := tx.ExecContext(ctx, `
insert into match_results(match_id, team_id, place, total, plus, metrics_json)
values(?, ?, ?, ?, ?, ?)
on conflict(match_id, team_id) do update set
  place = excluded.place,
  total = excluded.total,
  plus = excluded.plus,
  metrics_json = excluded.metrics_json`,
			match.MatchID, match.TeamIDs[index], place,
			int(outcome.Metrics["total"]), int(outcome.Metrics["plus"]), util.MustJSON(metrics)); err != nil {
			return err
		}
	}
	return nil
}

// effectivePlace applies the host's pin: a non-null place_override beats the
// scorer's place.
func effectivePlace(ctx context.Context, tx *sql.Tx, matchID, teamID int64, scored float64) (float64, error) {
	var override sql.NullFloat64
	err := tx.QueryRowContext(ctx,
		`select place_override from match_results where match_id = ? and team_id = ?`,
		matchID, teamID).Scan(&override)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	if override.Valid {
		return override.Float64, nil
	}
	return scored, nil
}
