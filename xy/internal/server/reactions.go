package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	xystrings "xy/i18nstrings"
)

// A Reaction is a timeline event whose payload is the (encrypted) emoji and
// whose reply_to_id is the comment it sits on — null when it sits on the card
// itself. It renders as chips, never as a timeline row, and un-reacting deletes
// the row outright: the one deviation from the everything-is-a-tombstone rule
// (ADR-0002), because a toggled-off reaction is not content anyone could miss.

type addReactionRequest struct {
	PayloadEnc string `json:"payload_enc"`
	TargetID   *int64 `json:"target_id"` // a comment on the same card; nil = the card
}

func (s *server) handleAddReaction(w http.ResponseWriter, r *http.Request) {
	uid, cardID, bid, ok := s.requireChildAccess(w, r, childCard)
	if !ok {
		return
	}
	var req addReactionRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.PayloadEnc == "" {
		httpError(w, http.StatusBadRequest, "payload_enc required")
		return
	}
	if req.TargetID != nil {
		var owner sql.NullInt64
		err := s.db.QueryRowContext(r.Context(), `
select card_id from timeline_events
where id = ? and type = 'comment' and deleted_at is null`, *req.TargetID).Scan(&owner)
		if errors.Is(err, sql.ErrNoRows) {
			httpError(w, http.StatusNotFound, xystrings.Default.Server.Comment.NotFound())
			return
		}
		if handleErr(w, err) {
			return
		}
		if !owner.Valid || owner.Int64 != cardID {
			httpError(w, http.StatusBadRequest, xystrings.Default.Server.Comment.Foreign())
			return
		}
	}
	var evID int64
	err := s.withWriteTx(r.Context(), "add-reaction", func(ctx context.Context, tx *sql.Tx) error {
		payload, err := unb64(req.PayloadEnc)
		if err != nil {
			return errBadRequest("invalid payload_enc")
		}
		evID, err = insertEvent(ctx, tx, timelineEvent{BoardID: bid, CardID: cardID, Type: "reaction", AuthorID: &uid, Payload: payload, ReplyToID: req.TargetID})
		return err
	})
	if handleErr(w, err) {
		return
	}
	writeJSON(w, map[string]int64{"id": evID})
}

func (s *server) handleDeleteReaction(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	var author sql.NullInt64
	err := s.db.QueryRowContext(r.Context(),
		`select author_user_id from timeline_events where id = ? and type = 'reaction'`, id).Scan(&author)
	if errors.Is(err, sql.ErrNoRows) {
		httpError(w, http.StatusNotFound, xystrings.Default.Server.Reaction.NotFound())
		return
	}
	if handleErr(w, err) {
		return
	}
	if !author.Valid || author.Int64 != u.UserID {
		httpError(w, http.StatusForbidden, xystrings.Default.Server.Reaction.DeleteOwnerOnly())
		return
	}
	err = s.withWriteTx(r.Context(), "delete-reaction", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `delete from timeline_events where id = ? and type = 'reaction'`, id)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
