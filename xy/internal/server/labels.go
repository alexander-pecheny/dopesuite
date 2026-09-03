package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	corei18n "pecheny.me/dopecore/i18nstrings"
	"time"
)

// A label is just a label (ADR-0004): what makes one a test's verdict rather
// than the author's is the ASSIGNMENT carrying a session, not the label itself.
type labelDTO struct {
	ID       int64  `json:"id"`
	NameEnc  string `json:"name_enc"`
	ColorEnc string `json:"color_enc"`
}

func scanLabels(ctx context.Context, q querier, boardID int64) ([]labelDTO, error) {
	rows, err := q.QueryContext(ctx, `
select id, name_enc, color_enc from labels where board_id = ? and deleted_at is null order by id`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []labelDTO{}
	for rows.Next() {
		var l labelDTO
		var nameEnc, colorEnc []byte
		if err := rows.Scan(&l.ID, &nameEnc, &colorEnc); err != nil {
			return nil, err
		}
		l.NameEnc = b64(nameEnc)
		l.ColorEnc = b64(colorEnc)
		out = append(out, l)
	}
	return out, rows.Err()
}

type createLabelRequest struct {
	NameEnc  string `json:"name_enc"`
	ColorEnc string `json:"color_enc"`
}

func (s *server) handleListLabels(w http.ResponseWriter, r *http.Request) {
	_, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	labels, err := scanLabels(r.Context(), s.db, bid)
	if handleErr(w, err) {
		return
	}
	writeJSON(w, labels)
}

func (s *server) handleCreateLabel(w http.ResponseWriter, r *http.Request) {
	_, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	var req createLabelRequest
	if !readJSON(w, r, &req) {
		return
	}
	nameEnc, err1 := unb64(req.NameEnc)
	colorEnc, err2 := unb64(req.ColorEnc)
	if err1 != nil || err2 != nil {
		httpError(w, http.StatusBadRequest, "invalid label fields")
		return
	}
	now := time.Now()
	var id int64
	err := s.withWriteTx(r.Context(), "create-label", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`insert into labels(board_id, name_enc, color_enc, created_at) values(?, ?, ?, ?)`,
			bid, nameEnc, colorEnc, rfc3339(now))
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

type patchLabelRequest struct {
	NameEnc  *string `json:"name_enc"`
	ColorEnc *string `json:"color_enc"`
}

func (s *server) handlePatchLabel(w http.ResponseWriter, r *http.Request) {
	_, labelID, _, ok := s.requireChildAccess(w, r, childLabel)
	if !ok {
		return
	}
	var req patchLabelRequest
	if !readJSON(w, r, &req) {
		return
	}
	err := s.withWriteTx(r.Context(), "patch-label", func(ctx context.Context, tx *sql.Tx) error {
		var p patch
		if req.NameEnc != nil {
			nameEnc, err := unb64(*req.NameEnc)
			if err != nil {
				return corei18n.User("invalid name_enc")
			}
			p.set("name_enc", nameEnc)
		}
		if req.ColorEnc != nil {
			colorEnc, err := unb64(*req.ColorEnc)
			if err != nil {
				return corei18n.User("invalid color_enc")
			}
			p.set("color_enc", colorEnc)
		}
		return p.apply(ctx, tx, "labels", labelID)
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleDeleteLabel(w http.ResponseWriter, r *http.Request) {
	u, authed := s.requireUser(w, r)
	if !authed {
		return
	}
	labelID, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	// A tombstone reads as already-deleted (204 no-op) rather than a 404.
	bid, err := childLabel.board(r.Context(), s.db, labelID)
	if errors.Is(err, sql.ErrNoRows) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if handleErr(w, err) {
		return
	}
	if _, err := boardRole(r.Context(), s.db, bid, u.UserID); handleErr(w, err) {
		return
	}
	err = s.withWriteTx(r.Context(), "delete-label", func(ctx context.Context, tx *sql.Tx) error {
		return tombstone(ctx, tx, "labels", "id = ?", labelID)
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
