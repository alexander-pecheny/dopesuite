package server

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

type listDTO struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	TitleEnc string `json:"title_enc"`
	Rank     string `json:"rank"`
	GroupID  *int64 `json:"group_id,omitempty"` // nil = not part of a list group
}

// groupDTO is a "list_of_lists": a named, ordered run of consecutive lists.
type groupDTO struct {
	ID      int64  `json:"id"`
	NameEnc string `json:"name_enc"`
}

func scanLists(ctx context.Context, q querier, boardID int64) ([]listDTO, error) {
	rows, err := q.QueryContext(ctx, `
select id, type, title_enc, rank, group_id from lists where board_id = ? and deleted_at is null order by rank`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []listDTO{}
	for rows.Next() {
		var l listDTO
		var titleEnc []byte
		var groupID sql.NullInt64
		if err := rows.Scan(&l.ID, &l.Type, &titleEnc, &l.Rank, &groupID); err != nil {
			return nil, err
		}
		l.TitleEnc = b64(titleEnc)
		if groupID.Valid {
			l.GroupID = &groupID.Int64
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// scanListGroups returns the board's non-deleted list groups.
func scanListGroups(ctx context.Context, q querier, boardID int64) ([]groupDTO, error) {
	rows, err := q.QueryContext(ctx, `
select id, name_enc from list_groups where board_id = ? and deleted_at is null order by id`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []groupDTO{}
	for rows.Next() {
		var g groupDTO
		var nameEnc []byte
		if err := rows.Scan(&g.ID, &nameEnc); err != nil {
			return nil, err
		}
		g.NameEnc = b64(nameEnc)
		out = append(out, g)
	}
	return out, rows.Err()
}

type createListRequest struct {
	TitleEnc string `json:"title_enc"`
	Rank     string `json:"rank"`
	Type     string `json:"type"`
}

func (s *server) handleCreateList(w http.ResponseWriter, r *http.Request) {
	_, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	var req createListRequest
	if !readJSON(w, r, &req) {
		return
	}
	titleEnc, err := unb64(req.TitleEnc)
	if err != nil || req.Rank == "" {
		httpError(w, http.StatusBadRequest, "invalid list fields")
		return
	}
	typ := req.Type
	if typ == "" {
		typ = "normal"
	}
	if typ != "normal" && typ != "test" {
		httpError(w, http.StatusBadRequest, "bad list type")
		return
	}
	now := time.Now()
	var id int64
	err = s.withWriteTx(r.Context(), "create-list", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
insert into lists(board_id, type, title_enc, rank, created_at, updated_at) values(?, ?, ?, ?, ?, ?)`,
			bid, typ, titleEnc, req.Rank, rfc3339(now), rfc3339(now))
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

type patchListRequest struct {
	TitleEnc *string `json:"title_enc"`
	Rank     *string `json:"rank"`
}

func (s *server) handlePatchList(w http.ResponseWriter, r *http.Request) {
	_, listID, _, ok := s.requireChildAccess(w, r, childList)
	if !ok {
		return
	}
	var req patchListRequest
	if !readJSON(w, r, &req) {
		return
	}
	err := s.withWriteTx(r.Context(), "patch-list", func(ctx context.Context, tx *sql.Tx) error {
		var p patch
		if req.TitleEnc != nil {
			titleEnc, err := unb64(*req.TitleEnc)
			if err != nil {
				return errBadRequest("invalid title_enc")
			}
			p.set("title_enc", titleEnc)
		}
		if req.Rank != nil {
			p.set("rank", *req.Rank)
		}
		return p.apply(ctx, tx, "lists", listID)
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleDeleteList(w http.ResponseWriter, r *http.Request) {
	_, listID, _, ok := s.requireChildAccess(w, r, childList)
	if !ok {
		return
	}
	err := s.withWriteTx(r.Context(), "delete-list", func(ctx context.Context, tx *sql.Tx) error {
		if err := tombstone(ctx, tx, "lists", "id = ?", listID); err != nil {
			return err
		}
		return tombstone(ctx, tx, "cards", "list_id = ?", listID)
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// A list group (list_of_lists) folds several lists into one tour.

type createListGroupRequest struct {
	NameEnc string  `json:"name_enc"`
	ListIDs []int64 `json:"list_ids"` // lists to fold into the new group
}

func (s *server) handleCreateListGroup(w http.ResponseWriter, r *http.Request) {
	_, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	var req createListGroupRequest
	if !readJSON(w, r, &req) {
		return
	}
	nameEnc, err := unb64(req.NameEnc)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid name_enc")
		return
	}
	if len(req.ListIDs) < 2 {
		httpError(w, http.StatusBadRequest, "нужно минимум два списка")
		return
	}
	now := time.Now()
	var id int64
	err = s.withWriteTx(r.Context(), "create-list-group", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `insert into list_groups(board_id, name_enc, created_at, updated_at) values(?, ?, ?, ?)`,
			bid, nameEnc, rfc3339(now), rfc3339(now))
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		for _, lid := range req.ListIDs {
			// Only fold in lists that actually belong to this board.
			res, err := tx.ExecContext(ctx, `update lists set group_id = ?, updated_at = ? where id = ? and board_id = ? and deleted_at is null`,
				id, rfc3339(now), lid, bid)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return errBadRequest("список не найден на этой доске")
			}
			if _, err := tx.ExecContext(ctx, `delete from tour_testers where list_id = ?`, lid); err != nil {
				return err
			}
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

type patchListGroupRequest struct {
	NameEnc *string `json:"name_enc"`
}

func (s *server) handlePatchListGroup(w http.ResponseWriter, r *http.Request) {
	_, groupID, _, ok := s.requireChildAccess(w, r, childGroup)
	if !ok {
		return
	}
	var req patchListGroupRequest
	if !readJSON(w, r, &req) {
		return
	}
	err := s.withWriteTx(r.Context(), "patch-list-group", func(ctx context.Context, tx *sql.Tx) error {
		var p patch
		if req.NameEnc != nil {
			nameEnc, err := unb64(*req.NameEnc)
			if err != nil {
				return errBadRequest("invalid name_enc")
			}
			p.set("name_enc", nameEnc)
		}
		return p.apply(ctx, tx, "list_groups", groupID)
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteListGroup dissolves a group: its member lists are released
// (group_id → NULL) and the group row is soft-deleted. The lists themselves stay.
func (s *server) handleDeleteListGroup(w http.ResponseWriter, r *http.Request) {
	_, groupID, _, ok := s.requireChildAccess(w, r, childGroup)
	if !ok {
		return
	}
	err := s.withWriteTx(r.Context(), "delete-list-group", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `update lists set group_id = null, updated_at = ? where group_id = ?`, rfc3339(time.Now()), groupID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `delete from tour_testers where group_id = ?`, groupID); err != nil {
			return err
		}
		return tombstone(ctx, tx, "list_groups", "id = ?", groupID)
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
