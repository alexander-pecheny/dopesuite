package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
	xystrings "xy/i18nstrings"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/cardkind"
)

// A vote is one member's "for" or "against" on an appeal card (ADR-0021). Who
// voted is plaintext like a comment's author; the value is an Envelope. A
// member has one vote per card, so casting again replaces it.

type voteDTO struct {
	CardID int64  `json:"card_id"`
	UserID int64  `json:"user_id"`
	Value  string `json:"value"`
}

func scanVotes(ctx context.Context, q querier, boardID int64) ([]voteDTO, error) {
	rows, err := q.QueryContext(ctx, `
select v.card_id, v.user_id, v.value
from card_votes v join cards c on c.id = v.card_id
where c.board_id = ? and c.deleted_at is null order by v.card_id, v.user_id`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []voteDTO{}
	for rows.Next() {
		var v voteDTO
		var value []byte
		if err := rows.Scan(&v.CardID, &v.UserID, &value); err != nil {
			return nil, err
		}
		v.Value = b64(value)
		out = append(out, v)
	}
	return out, rows.Err()
}

// requireAppeal refuses a vote on any card but an appeal: on a question that
// job is a Label's.
func requireAppeal(ctx context.Context, tx *sql.Tx, cardID int64) error {
	var kind string
	err := tx.QueryRowContext(ctx, `select kind from cards where id = ?`, cardID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && kind != cardkind.Appeal) {
		return corei18n.User(xystrings.Default.Server.Appeal.VoteNotAppeal())
	}
	return err
}

func (s *server) handlePutVote(w http.ResponseWriter, r *http.Request) {
	uid, cardID, _, ok := s.requireChildAccess(w, r, childCard)
	if !ok {
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	value, err := unb64(req.Value)
	if err != nil || len(value) == 0 {
		httpError(w, http.StatusBadRequest, "invalid value")
		return
	}
	err = s.withWriteTx(r.Context(), "put-vote", func(ctx context.Context, tx *sql.Tx) error {
		if err := requireAppeal(ctx, tx, cardID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
insert into card_votes(card_id, user_id, value, updated_at) values(?, ?, ?, ?)
on conflict(card_id, user_id) do update set value = excluded.value, updated_at = excluded.updated_at`,
			cardID, uid, value, rfc3339(time.Now()))
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleDeleteVote(w http.ResponseWriter, r *http.Request) {
	uid, cardID, _, ok := s.requireChildAccess(w, r, childCard)
	if !ok {
		return
	}
	err := s.withWriteTx(r.Context(), "delete-vote", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `delete from card_votes where card_id = ? and user_id = ?`, cardID, uid)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
