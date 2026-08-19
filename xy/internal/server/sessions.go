package server

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

// Test sessions: one sitting at which a group of testers played a set of
// questions. Board-level, not a card in a list — see docs/labels-redesign.md and
// ADR-0003. meta_enc is the whole session (date, time, zone, cities, title,
// testers, key, origin) as one envelope; the server never looks inside.

type sessionDTO struct {
	ID        int64  `json:"id"`
	MetaEnc   string `json:"meta_enc"`
	CreatedAt string `json:"created_at"`
}

func scanSessions(ctx context.Context, q querier, boardID int64) ([]sessionDTO, error) {
	rows, err := q.QueryContext(ctx, `
select id, meta_enc, created_at from test_sessions where board_id = ? and deleted_at is null order by id`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sessionDTO{}
	for rows.Next() {
		var s sessionDTO
		var metaEnc []byte
		if err := rows.Scan(&s.ID, &metaEnc, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.MetaEnc = b64(metaEnc)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (s *server) requireSession(w http.ResponseWriter, r *http.Request) (sessionID, boardID int64, ok bool) {
	u, authed := s.requireUser(w, r)
	if !authed {
		return 0, 0, false
	}
	sessionID, ok = pathInt(w, r, "id")
	if !ok {
		return 0, 0, false
	}
	boardID, err := boardOf(r.Context(), s.db, childSession, sessionID)
	if handleErr(w, err) {
		return 0, 0, false
	}
	if _, err := boardRole(r.Context(), s.db, boardID, u.UserID); handleErr(w, err) {
		return 0, 0, false
	}
	return sessionID, boardID, true
}

type sessionRequest struct {
	MetaEnc string `json:"meta_enc"`
}

func (s *server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	_, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	sessions, err := scanSessions(r.Context(), s.db, bid)
	if handleErr(w, err) {
		return
	}
	writeJSON(w, sessions)
}

func (s *server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	_, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	var req sessionRequest
	if !readJSON(w, r, &req) {
		return
	}
	metaEnc, err := unb64(req.MetaEnc)
	if err != nil || len(metaEnc) == 0 {
		httpError(w, http.StatusBadRequest, "invalid meta_enc")
		return
	}
	now := time.Now()
	var id int64
	err = s.withWriteTx(r.Context(), "create-session", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`insert into test_sessions(board_id, meta_enc, created_at) values(?, ?, ?)`,
			bid, metaEnc, rfc3339(now))
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if handleErr(w, err) {
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

func (s *server) handlePatchSession(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	var req sessionRequest
	if !readJSON(w, r, &req) {
		return
	}
	metaEnc, err := unb64(req.MetaEnc)
	if err != nil || len(metaEnc) == 0 {
		httpError(w, http.StatusBadRequest, "invalid meta_enc")
		return
	}
	err = s.withWriteTx(r.Context(), "patch-session", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update test_sessions set meta_enc = ? where id = ?`, metaEnc, sessionID)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteSession tombstones the session; its labels and session-only
// comments follow by FK cascade at reap time, and disappear from the board at
// once because every read joins on deleted_at.
func (s *server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	now := rfc3339(time.Now())
	err := s.withWriteTx(r.Context(), "delete-session", func(ctx context.Context, tx *sql.Tx) error {
		if err := tombstone(ctx, tx, "test_sessions", "id = ?", sessionID); err != nil {
			return err
		}
		// A label OUTLIVES its session: it is an ordinary board label. What goes is
		// the playings on it and the assignments scoped to them — a label scoped to
		// a playing that no longer exists cannot be read (ADR-0004).
		if _, err := tx.ExecContext(ctx, `delete from card_labels where session_id = ?`, sessionID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `delete from card_sessions where session_id = ?`, sessionID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`update timeline_events set deleted_at = ? where session_id = ? and card_id is null and deleted_at is null`,
			now, sessionID)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleGetSessionTimeline(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	out, err := s.readTimeline(r.Context(), `
left join cards c on c.id = e.card_id
where e.session_id = ? and e.deleted_at is null
  and (e.card_id is null or c.deleted_at is null)
order by e.id`, sessionID)
	if handleErr(w, err) {
		return
	}
	writeJSON(w, out)
}

func (s *server) handleAddSessionComment(w http.ResponseWriter, r *http.Request) {
	u, authed := s.requireUser(w, r)
	if !authed {
		return
	}
	sessionID, bid, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	var req addCommentRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.PayloadEnc == "" {
		httpError(w, http.StatusBadRequest, "payload_enc required")
		return
	}
	err := s.withWriteTx(r.Context(), "add-session-comment", func(ctx context.Context, tx *sql.Tx) error {
		payload, err := unb64(req.PayloadEnc)
		if err != nil {
			return errBadRequest("invalid payload_enc")
		}
		_, err = insertEvent(ctx, tx, timelineEvent{BoardID: bid, SessionID: &sessionID, Type: "comment", AuthorID: &u.UserID, Payload: payload})
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
