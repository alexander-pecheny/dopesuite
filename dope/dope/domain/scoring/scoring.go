// Package scoring materialises a match's Protocol outcome into match_results —
// the only rows the Structure layer reads (docs/unified-model.md §2), and the
// one place that writes them. Places are the scorer's, unless the host pinned
// one in the match's state blob (ADR-0005: auto-places with manual override,
// the Pin being Protocol state).
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

// RecalculateMatchResultsTx scores a match through its game's registered
// Protocol and upserts match_results for every occupied slot, honouring pins.
func RecalculateMatchResultsTx(ctx context.Context, tx *sql.Tx, match store.DBMatchState) error {
	// The Game's scheme is what a flat Protocol reads its shape from — OD's
	// tour composition, KSI's stickers; the Match Protocols read nothing.
	var protocolCode, schemeJSON string
	if err := tx.QueryRowContext(ctx,
		`select game_type, coalesce(scheme_json, '') from games where id = ?`, match.GameID).Scan(&protocolCode, &schemeJSON); err != nil {
		return err
	}
	p, ok := protocol.Get(protocolCode)
	if !ok {
		return fmt.Errorf("scoring: no protocol %q", protocolCode)
	}
	outcomes, err := p.Score(json.RawMessage(schemeJSON), match.ProtocolState())
	if err != nil {
		return err
	}
	for index, outcome := range outcomes {
		if index >= len(match.ParticipantIDs) || match.ParticipantIDs[index] == 0 {
			continue
		}
		place := outcome.Place
		if pin := match.Blob.Pin(match.ParticipantIDs[index]); pin != nil {
			place = *pin
		}
		if _, err := tx.ExecContext(ctx, `
insert into match_results(match_id, participant_id, place, total, plus, tiebreak, metrics_json)
values(?, ?, ?, ?, ?, ?, ?)
on conflict(match_id, participant_id) do update set
  place = excluded.place,
  total = excluded.total,
  plus = excluded.plus,
  tiebreak = excluded.tiebreak,
  metrics_json = excluded.metrics_json`,
			match.MatchID, match.ParticipantIDs[index], place,
			int(outcome.Metrics["total"]), int(outcome.Metrics["plus"]), int(outcome.Metrics["tiebreak"]),
			util.MustJSON(outcome.Metrics)); err != nil {
			return err
		}
	}
	return nil
}
