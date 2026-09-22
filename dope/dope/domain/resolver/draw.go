package resolver

import (
	"context"
	"database/sql"
	"errors"

	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"
	corei18n "pecheny.me/dopecore/i18nstrings"
)

// The Draw a host makes in the middle of a Game: Hamsa seats the three
// fourth-place teams of Game 1 by a blind draw before Game 2. The Slot is
// declared as drawn by the Kind, the resolver never fills it, and the host's
// choice is written straight onto the Slot — that is the whole distinction
// between a Draw and a derived seating (CONTEXT.md, «Draw»).

// ErrDrawSlotNotFound names a draw code this Game has no Slot for.
var ErrDrawSlotNotFound = errors.New("draw slot not found")

type drawSlotRow struct {
	slotID   int64
	matchID  int64
	stageID  int64
	draw     *store.SchemeDraw
	occupant int64
}

// SetDrawTx seats a Participant in a Draw Slot, or clears it when participant
// is zero. It refuses anyone the Slot does not name as a candidate, and anyone
// already drawn into another Slot of the same stage: a team plays one table
// per Round, and a draw that seated it twice would leave a table short.
func SetDrawTx(ctx context.Context, tx *sql.Tx, gameID int64, code string, participant int64) ([]int64, error) {
	s := dopestrings.Default
	slots, err := drawSlotsTx(ctx, tx, gameID)
	if err != nil {
		return nil, err
	}
	var slot drawSlotRow
	for _, row := range slots {
		if row.draw.Code == code {
			slot = row
			break
		}
	}
	if slot.slotID == 0 {
		return nil, ErrDrawSlotNotFound
	}
	if participant != 0 {
		candidates, err := store.LoadDrawCandidates(ctx, tx, gameID, slot.draw)
		if err != nil {
			return nil, err
		}
		eligible := false
		for _, candidate := range candidates {
			eligible = eligible || candidate.ID == participant
		}
		if !eligible {
			return nil, corei18n.User(s.Resolver.Draw.NotACandidate())
		}
		for _, other := range slots {
			if other.slotID != slot.slotID && other.stageID == slot.stageID && other.occupant == participant {
				return nil, corei18n.User(s.Resolver.Draw.AlreadySeated())
			}
		}
	}
	if slot.occupant == participant {
		return nil, nil
	}
	// A Match whose seats changed is reopened, exactly as a resolved seat does
	// it, so its standings are reviewed rather than left over from whoever sat
	// there before.
	if slot.occupant != 0 {
		if _, err := tx.ExecContext(ctx, `update matches set status = 'active' where id = ? and status = 'finished'`, slot.matchID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
update match_slots set participant_id = ?, locked = ? where id = ?`,
		nullableInt64(participant), boolInt(participant != 0), slot.slotID); err != nil {
		return nil, err
	}
	affected, err := ResolveGameSlotsTx(ctx, tx, gameID)
	if err != nil {
		return nil, err
	}
	return append([]int64{slot.matchID}, affected...), nil
}

// drawSlotsTx reads the Game's Draw Slots. They are stored as placeholders —
// a seat no rule derives is exactly what a placeholder is — with the draw and
// its candidates in the ref.
func drawSlotsTx(ctx context.Context, q store.Queryer, gameID int64) ([]drawSlotRow, error) {
	rows, err := store.CollectRows(ctx, q, `
select ms.id, ms.match_id, m.stage_id, ms.source_ref_json, coalesce(ms.participant_id, 0)
from match_slots ms
join matches m on m.id = ms.match_id
where m.game_id = ? and ms.source_type = ?
order by ms.match_id, ms.slot_index`,
		[]any{gameID, store.SlotPlaceholder}, func(rows *sql.Rows) (drawSlotRow, error) {
			var row drawSlotRow
			var ref string
			if err := rows.Scan(&row.slotID, &row.matchID, &row.stageID, &ref, &row.occupant); err != nil {
				return row, err
			}
			row.draw = store.ParseSlotRef(store.SlotPlaceholder, ref).Draw
			return row, nil
		})
	if err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, row := range rows {
		if row.draw != nil && row.draw.Code != "" {
			out = append(out, row)
		}
	}
	return out, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
