package server

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
	xystrings "xy/i18nstrings"
)

type listDTO struct {
	ID      int64  `json:"id"`
	Type    string `json:"type"`
	Title   string `json:"title"`
	Rank    string `json:"rank"`
	GroupID *int64 `json:"group_id,omitempty"` // nil = not part of a list group
	// CommentFirst puts a question's comment before its answer in every
	// rendering. A grouped list follows its group's flag instead of its own.
	CommentFirst bool `json:"comment_first"`
}

// groupDTO is a "list_of_lists": a named, ordered run of consecutive lists.
type groupDTO struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	CommentFirst bool   `json:"comment_first"`
}

func scanLists(ctx context.Context, q querier, boardID int64) ([]listDTO, error) {
	rows, err := q.QueryContext(ctx, `
select id, type, title, rank, group_id, comment_first from lists where board_id = ? and deleted_at is null order by rank`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []listDTO{}
	for rows.Next() {
		var l listDTO
		var title []byte
		var groupID sql.NullInt64
		if err := rows.Scan(&l.ID, &l.Type, &title, &l.Rank, &groupID, &l.CommentFirst); err != nil {
			return nil, err
		}
		l.Title = b64(title)
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
select id, name, comment_first from list_groups where board_id = ? and deleted_at is null order by id`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []groupDTO{}
	for rows.Next() {
		var g groupDTO
		var name []byte
		if err := rows.Scan(&g.ID, &name, &g.CommentFirst); err != nil {
			return nil, err
		}
		g.Name = b64(name)
		out = append(out, g)
	}
	return out, rows.Err()
}

type createListRequest struct {
	Title        string `json:"title"`
	Rank         string `json:"rank"`
	Type         string `json:"type"`
	CommentFirst bool   `json:"comment_first"`
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
	title, err := unb64(req.Title)
	if err != nil || req.Rank == "" {
		httpError(w, http.StatusBadRequest, "invalid list fields")
		return
	}
	typ := req.Type
	if typ == "" {
		typ = "normal"
	}
	if !validListType(typ) {
		httpError(w, http.StatusBadRequest, "bad list type")
		return
	}
	now := time.Now()
	var id int64
	err = s.withWriteTx(r.Context(), "create-list", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
insert into lists(board_id, type, title, rank, comment_first, created_at, updated_at) values(?, ?, ?, ?, ?, ?, ?)`,
			bid, typ, title, req.Rank, req.CommentFirst, rfc3339(now), rfc3339(now))
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

// validListType allowlists the list types the client may set (mirrors the
// lists.type CHECK constraint). 'si' is a list of SI theme's, 'normal' one of OD
// questions; the type decides what kind of card the add-card button makes, and
// nothing else.
func validListType(typ string) bool {
	switch typ {
	case "normal", "test", "si":
		return true
	}
	return false
}

type patchListRequest struct {
	Title        *string `json:"title"`
	Rank         *string `json:"rank"`
	Type         *string `json:"type"`
	CommentFirst *bool   `json:"comment_first"`
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
		if req.Title != nil {
			title, err := unb64(*req.Title)
			if err != nil {
				return corei18n.User("invalid title")
			}
			p.set("title", title)
		}
		if req.Rank != nil {
			p.set("rank", *req.Rank)
		}
		if req.Type != nil {
			if !validListType(*req.Type) {
				return corei18n.User("bad list type")
			}
			p.set("type", *req.Type)
		}
		if req.CommentFirst != nil {
			p.set("comment_first", *req.CommentFirst)
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
	Name    string  `json:"name"`
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
	name, err := unb64(req.Name)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid name")
		return
	}
	if len(req.ListIDs) < 2 {
		httpError(w, http.StatusBadRequest, xystrings.Default.Server.List.GroupNeedsTwo())
		return
	}
	now := time.Now()
	var id int64
	err = s.withWriteTx(r.Context(), "create-list-group", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
insert into list_groups(board_id, name, comment_first, created_at, updated_at)
values(?, ?, coalesce((select comment_first from lists where id = ? and board_id = ?), 0), ?, ?)`,
			bid, name, req.ListIDs[0], bid, rfc3339(now), rfc3339(now))
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
				return corei18n.User(xystrings.Default.Server.List.NotOnBoard())
			}
			if _, err := tx.ExecContext(ctx, `delete from tour_testers where list_id = ?`, lid); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `delete from tour_declarations where list_id = ?`, lid); err != nil {
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
	Name         *string `json:"name"`
	CommentFirst *bool   `json:"comment_first"`
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
		if req.Name != nil {
			name, err := unb64(*req.Name)
			if err != nil {
				return corei18n.User("invalid name")
			}
			p.set("name", name)
		}
		if req.CommentFirst != nil {
			p.set("comment_first", *req.CommentFirst)
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
		if _, err := tx.ExecContext(ctx, `
update lists set group_id = null, updated_at = ?,
  comment_first = (select comment_first from list_groups where id = ?)
where group_id = ?`, rfc3339(time.Now()), groupID, groupID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `delete from tour_testers where group_id = ?`, groupID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `delete from tour_declarations where group_id = ?`, groupID); err != nil {
			return err
		}
		return tombstone(ctx, tx, "list_groups", "id = ?", groupID)
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
