// Package flatgame keeps a flat game — one Block, one Match, the whole document
// on it (ChGK, KSI) — a Structure like every other. A flat document changes
// two ways, SetStateTx and PatchStateTx, and both end the same way: the Match's
// seats follow the document's team list, the Protocol scores the document
// into match_results, and the Block ranks into stage_standings. SettleTx is
// that ending on its own, for a game just built, cleared or migrated.
package flatgame

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"dope/dope/domain/protocol"
	"dope/dope/domain/resolver"
	"dope/dope/domain/scoring"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
)

// ErrNotFlat is a game whose Protocol keeps no document on one Match.
var ErrNotFlat = errors.New("flatgame: not a flat format")

// SetStateTx replaces the whole document, journalled as one replace op.
func SetStateTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, raw string) error {
	matchID, err := store.FlatMatchID(ctx, tx, gameID)
	if err != nil {
		return err
	}
	if err := festwrite.SetFlatGameStateTx(ctx, tx, matchID, raw); err != nil {
		return err
	}
	return settleTx(ctx, tx, festID, gameID, matchID)
}

// SaveDocumentTx writes a Game's document where it lives (store.GameDoc): on
// its 'main' Match, journalled as the ops that made it (or as one replace when
// ops is nil) and settled; or, for a Game without one, on the game row.
func SaveDocumentTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, matchID sql.NullInt64, next string, ops []store.BlobOp) error {
	if !matchID.Valid {
		result, err := tx.ExecContext(ctx, `
update games set state_json = ?, updated_at = ? where fest_id = ? and id = ?`, next, util.UtcNow(), festID, gameID)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return sql.ErrNoRows
		}
		return nil
	}
	if ops == nil {
		if err := festwrite.SetFlatGameStateTx(ctx, tx, matchID.Int64, next); err != nil {
			return err
		}
		return settleTx(ctx, tx, festID, gameID, matchID.Int64)
	}
	return PatchStateTx(ctx, tx, festID, gameID, matchID.Int64, next, ops)
}

// PatchStateTx stores a document the caller already patched, journalling the
// ops that made it.
func PatchStateTx(ctx context.Context, tx *sql.Tx, festID, gameID, matchID int64, next string, ops []store.BlobOp) error {
	if _, err := tx.ExecContext(ctx, `update matches set state_json = ? where id = ?`, next, matchID); err != nil {
		return err
	}
	if err := festwrite.JournalMatchPatchTx(ctx, tx, matchID, ops); err != nil {
		return err
	}
	return settleTx(ctx, tx, festID, gameID, matchID)
}

// SettleTx seats, scores and ranks a flat game from the document it holds.
func SettleTx(ctx context.Context, tx *sql.Tx, festID, gameID int64) error {
	matchID, err := store.FlatMatchID(ctx, tx, gameID)
	if err != nil {
		return err
	}
	return settleTx(ctx, tx, festID, gameID, matchID)
}

func settleTx(ctx context.Context, tx *sql.Tx, festID, gameID, matchID int64) error {
	if _, err := tx.ExecContext(ctx, `update games set updated_at = ? where id = ?`, util.UtcNow(), gameID); err != nil {
		return err
	}
	var gameType, state string
	if err := tx.QueryRowContext(ctx, `
select g.game_type, m.state_json from matches m join games g on g.id = m.game_id where m.id = ?`, matchID).Scan(&gameType, &state); err != nil {
		return err
	}
	seats, ok := protocol.Seats(gameType, json.RawMessage(state))
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFlat, gameType)
	}
	if err := seatTx(ctx, tx, festID, gameID, matchID, seats); err != nil {
		return err
	}
	match, err := store.LoadMatchState(ctx, tx, store.MatchSelector{FestID: festID, GameID: gameID, MatchID: matchID})
	if err != nil {
		return err
	}
	if err := scoring.RecalculateMatchResultsTx(ctx, tx, match); err != nil {
		return err
	}
	_, err = resolver.ResolveGameSlotsTx(ctx, tx, gameID)
	return err
}

// seatTx makes the Match's slots the document's team list: seat i is the
// Participant playing under the i-th team's number, minted or renamed as the
// document says; a team without a number sits in an empty seat. The Game's
// entrant list follows when every seat is numbered, and is dropped otherwise
// so the numbering guard falls back to the fest's registry.
func seatTx(ctx context.Context, tx *sql.Tx, festID, gameID, matchID int64, seats []protocol.Seat) error {
	wanted := make([]int64, len(seats))
	numbered := true
	for i, seat := range seats {
		if seat.Number <= 0 || seat.Name == "" {
			numbered = false
			continue
		}
		id, err := store.EnsureParticipantByNumber(ctx, tx, festID, "team", seat.Number, seat.Name, seat.City)
		if err != nil {
			return err
		}
		wanted[i] = id
	}
	current, err := store.CollectRows(ctx, tx, `
select coalesce(participant_id, 0) from match_slots where match_id = ? order by slot_index`, []any{matchID},
		func(rows *sql.Rows) (int64, error) {
			var id int64
			err := rows.Scan(&id)
			return id, err
		})
	if err != nil {
		return err
	}
	if len(current) == len(wanted) {
		same := true
		for i := range wanted {
			same = same && current[i] == wanted[i]
		}
		if same {
			return nil
		}
	}
	for _, q := range []string{
		`delete from match_slots where match_id = ?`,
		`delete from match_results where match_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, matchID); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`delete from game_assignments where game_id = ?`,
		`delete from game_participants where game_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, gameID); err != nil {
			return err
		}
	}
	for i, seat := range seats {
		ref := store.SeedRef(int(seat.Number)).JSON()
		if _, err := tx.ExecContext(ctx, `
insert into match_slots(match_id, slot_index, source_type, source_ref_json, participant_id, locked)
values(?, ?, 'seed', ?, ?, 0)`, matchID, i, ref, util.NullableInt64(wanted[i])); err != nil {
			return err
		}
		if wanted[i] == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
insert into game_assignments(game_id, basket, number, participant_id) values(?, 1, ?, ?)
on conflict(game_id, basket, number) do update set participant_id = excluded.participant_id`,
			gameID, seat.Number, wanted[i]); err != nil {
			return err
		}
		if numbered {
			if _, err := tx.ExecContext(ctx, `
insert into game_participants(game_id, participant_id, position, number) values(?, ?, ?, ?)
on conflict(game_id, participant_id) do update set position = excluded.position, number = excluded.number`,
				gameID, wanted[i], i+1, seat.Number); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `update matches set participant_count = ? where id = ?`, len(seats), matchID)
	return err
}
