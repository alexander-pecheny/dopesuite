package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
)

// A board's children — lists, list groups, cards, labels, tests — each carry a
// board_id and a tombstone. child names one table and how its absence reads;
// boardOf and onBoard are the two questions every handler asks of a child.
type child struct {
	table    string
	notFound string
	foreign  string
}

var (
	childCard    = child{"cards", "карточка не найдена", "карточка с другой доски"}
	childList    = child{"lists", "список не найден", "список с другой доски"}
	childGroup   = child{"list_groups", "группа списков не найдена", "группа с другой доски"}
	childLabel   = child{"labels", "метка не найдена", "метка с другой доски"}
	childSession = child{"test_sessions", "тест не найден", "тест с другой доски"}
)

func (c child) board(ctx context.Context, q querier, id int64) (int64, error) {
	var bid int64
	err := q.QueryRowContext(ctx, `select board_id from `+c.table+` where id = ? and deleted_at is null`, id).Scan(&bid)
	return bid, err
}

// boardOf is the owning board of a live child named in the path, or a 404.
func boardOf(ctx context.Context, q querier, c child, id int64) (int64, error) {
	bid, err := c.board(ctx, q, id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errNotFound(c.notFound)
	}
	return bid, err
}

// onBoard checks that a child named in a request body is live and on the
// caller's board. Either way it is a 400: the request is wrong, not the path.
func onBoard(ctx context.Context, q querier, c child, id, bid int64) error {
	owner, err := c.board(ctx, q, id)
	if errors.Is(err, sql.ErrNoRows) {
		return errBadRequest(c.notFound)
	}
	if err != nil {
		return err
	}
	if owner != bid {
		return errBadRequest(c.foreign)
	}
	return nil
}

// requireChildAccess resolves the user, the `id` path param and the child's
// board, and checks the user is on that board. A tombstone is a 404 here;
// handleDeleteLabel keeps its own 204-on-tombstone by hand.
func (s *server) requireChildAccess(w http.ResponseWriter, r *http.Request, c child) (userID, childID, boardID int64, ok bool) {
	u, authed := s.requireUser(w, r)
	if !authed {
		return 0, 0, 0, false
	}
	cid, okp := pathInt(w, r, "id")
	if !okp {
		return 0, 0, 0, false
	}
	bid, err := boardOf(r.Context(), s.db, c, cid)
	if handleErr(w, err) {
		return 0, 0, 0, false
	}
	if _, err := boardRole(r.Context(), s.db, bid, u.UserID); handleErr(w, err) {
		return 0, 0, 0, false
	}
	return u.UserID, cid, bid, true
}
