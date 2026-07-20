package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"
)

// ---- DTOs + scanners ----

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

type cardDTO struct {
	ID             int64   `json:"id"`
	ListID         int64   `json:"list_id"`
	Kind           string  `json:"kind"`
	DescEnc        string  `json:"description_enc"`
	Rank           string  `json:"rank"`
	HandoutMetaEnc *string `json:"handout_meta_enc,omitempty"` // nil = no saved handout settings
	AliasEnc       *string `json:"alias_enc,omitempty"`        // nil = no alias
	// CreatedAt anchors the лента: the client shows it as a «карточка создана»
	// line under the oldest event, so every later timestamp has something to be
	// read against. Deliberately NOT a timeline event — the column already
	// exists on every card ever made, where an event would only cover new ones.
	CreatedAt string `json:"created_at"`
}

type labelDTO struct {
	ID       int64  `json:"id"`
	NameEnc  string `json:"name_enc"`
	ColorEnc string `json:"color_enc"`
	Kind     string `json:"kind"`
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

func scanCards(ctx context.Context, q querier, boardID int64) ([]cardDTO, error) {
	rows, err := q.QueryContext(ctx, `
select id, list_id, kind, description_enc, rank, handout_meta_enc, alias_enc, created_at
from cards where board_id = ? and deleted_at is null order by rank`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cardDTO{}
	for rows.Next() {
		var c cardDTO
		var descEnc, metaEnc, aliasEnc []byte
		if err := rows.Scan(&c.ID, &c.ListID, &c.Kind, &descEnc, &c.Rank, &metaEnc, &aliasEnc, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.DescEnc = b64(descEnc)
		if metaEnc != nil {
			s := b64(metaEnc)
			c.HandoutMetaEnc = &s
		}
		if aliasEnc != nil {
			s := b64(aliasEnc)
			c.AliasEnc = &s
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanLabels(ctx context.Context, q querier, boardID int64) ([]labelDTO, error) {
	rows, err := q.QueryContext(ctx, `
select id, name_enc, color_enc, kind from labels where board_id = ? and deleted_at is null order by id`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []labelDTO{}
	for rows.Next() {
		var l labelDTO
		var nameEnc, colorEnc []byte
		if err := rows.Scan(&l.ID, &nameEnc, &colorEnc, &l.Kind); err != nil {
			return nil, err
		}
		l.NameEnc = b64(nameEnc)
		l.ColorEnc = b64(colorEnc)
		out = append(out, l)
	}
	return out, rows.Err()
}

// boardOfCard / boardOfList resolve the owning board (for ACL) of a child entity.
func boardOfCard(ctx context.Context, q querier, cardID int64) (int64, error) {
	var bid int64
	err := q.QueryRowContext(ctx, `select board_id from cards where id = ? and deleted_at is null`, cardID).Scan(&bid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errNotFound("карточка не найдена")
	}
	return bid, err
}

func boardOfList(ctx context.Context, q querier, listID int64) (int64, error) {
	var bid int64
	err := q.QueryRowContext(ctx, `select board_id from lists where id = ? and deleted_at is null`, listID).Scan(&bid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errNotFound("список не найден")
	}
	return bid, err
}

// requireCardAccess / requireListAccess resolve the user and verify board access
// for a card/list path param.
func (s *server) requireChildAccess(w http.ResponseWriter, r *http.Request, resolve func(context.Context, querier, int64) (int64, error)) (userID, childID, boardID int64, ok bool) {
	u, authed := s.requireUser(w, r)
	if !authed {
		return 0, 0, 0, false
	}
	cid, okp := pathInt(w, r, "id")
	if !okp {
		return 0, 0, 0, false
	}
	bid, err := resolve(r.Context(), s.db, cid)
	if handleErr(w, err) {
		return 0, 0, 0, false
	}
	if _, err := boardRole(r.Context(), s.db, bid, u.UserID); handleErr(w, err) {
		return 0, 0, 0, false
	}
	return u.UserID, cid, bid, true
}

// ---- lists ----

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
	_, listID, _, ok := s.requireChildAccess(w, r, boardOfList)
	if !ok {
		return
	}
	var req patchListRequest
	if !readJSON(w, r, &req) {
		return
	}
	err := s.withWriteTx(r.Context(), "patch-list", func(ctx context.Context, tx *sql.Tx) error {
		if req.TitleEnc != nil {
			titleEnc, err := unb64(*req.TitleEnc)
			if err != nil {
				return errBadRequest("invalid title_enc")
			}
			if _, err := tx.ExecContext(ctx, `update lists set title_enc = ?, updated_at = ? where id = ?`, titleEnc, rfc3339(time.Now()), listID); err != nil {
				return err
			}
		}
		if req.Rank != nil {
			if _, err := tx.ExecContext(ctx, `update lists set rank = ?, updated_at = ? where id = ?`, *req.Rank, rfc3339(time.Now()), listID); err != nil {
				return err
			}
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleDeleteList(w http.ResponseWriter, r *http.Request) {
	_, listID, _, ok := s.requireChildAccess(w, r, boardOfList)
	if !ok {
		return
	}
	err := s.withWriteTx(r.Context(), "delete-list", func(ctx context.Context, tx *sql.Tx) error {
		now := rfc3339(time.Now())
		if _, err := tx.ExecContext(ctx, `update lists set deleted_at = ? where id = ?`, now, listID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `update cards set deleted_at = ? where list_id = ? and deleted_at is null`, now, listID)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- list groups (list_of_lists) ----

func boardOfListGroup(ctx context.Context, q querier, groupID int64) (int64, error) {
	var bid int64
	err := q.QueryRowContext(ctx, `select board_id from list_groups where id = ? and deleted_at is null`, groupID).Scan(&bid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errNotFound("группа списков не найдена")
	}
	return bid, err
}

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
	_, groupID, _, ok := s.requireChildAccess(w, r, boardOfListGroup)
	if !ok {
		return
	}
	var req patchListGroupRequest
	if !readJSON(w, r, &req) {
		return
	}
	err := s.withWriteTx(r.Context(), "patch-list-group", func(ctx context.Context, tx *sql.Tx) error {
		if req.NameEnc != nil {
			nameEnc, err := unb64(*req.NameEnc)
			if err != nil {
				return errBadRequest("invalid name_enc")
			}
			if _, err := tx.ExecContext(ctx, `update list_groups set name_enc = ?, updated_at = ? where id = ?`, nameEnc, rfc3339(time.Now()), groupID); err != nil {
				return err
			}
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteListGroup dissolves a group: its member lists are released
// (group_id → NULL) and the group row is soft-deleted. The lists themselves stay.
func (s *server) handleDeleteListGroup(w http.ResponseWriter, r *http.Request) {
	_, groupID, _, ok := s.requireChildAccess(w, r, boardOfListGroup)
	if !ok {
		return
	}
	err := s.withWriteTx(r.Context(), "delete-list-group", func(ctx context.Context, tx *sql.Tx) error {
		now := rfc3339(time.Now())
		if _, err := tx.ExecContext(ctx, `update lists set group_id = null, updated_at = ? where group_id = ?`, now, groupID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `update list_groups set deleted_at = ? where id = ?`, now, groupID)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- cards ----

type createCardRequest struct {
	DescEnc        string  `json:"description_enc"`
	Rank           string  `json:"rank"`
	Kind           string  `json:"kind"`
	HandoutMetaEnc *string `json:"handout_meta_enc"` // optional handout-gen settings
	AliasEnc       *string `json:"alias_enc"`        // optional short label shown instead of the card's text
}

// validCardKind allowlists the card kinds the client may set (mirrors the
// cards.kind CHECK constraint).
func validCardKind(kind string) bool {
	switch kind {
	case "normal", "question", "test", "meta", "heading", "other":
		return true
	}
	return false
}

func (s *server) handleCreateCard(w http.ResponseWriter, r *http.Request) {
	// path param is the list id
	u, authed := s.requireUser(w, r)
	if !authed {
		return
	}
	listID, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	bid, err := boardOfList(r.Context(), s.db, listID)
	if handleErr(w, err) {
		return
	}
	if _, err := boardRole(r.Context(), s.db, bid, u.UserID); handleErr(w, err) {
		return
	}
	var req createCardRequest
	if !readJSON(w, r, &req) {
		return
	}
	descEnc, err := unb64(req.DescEnc)
	if err != nil || req.Rank == "" {
		httpError(w, http.StatusBadRequest, "invalid card fields")
		return
	}
	kind := req.Kind
	if kind == "" {
		kind = "normal"
	}
	if !validCardKind(kind) {
		httpError(w, http.StatusBadRequest, "bad card kind")
		return
	}
	metaEnc, err := optBlob(req.HandoutMetaEnc)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid handout_meta_enc")
		return
	}
	aliasEnc, err := optBlob(req.AliasEnc)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid alias_enc")
		return
	}
	now := time.Now()
	var id int64
	err = s.withWriteTx(r.Context(), "create-card", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
insert into cards(board_id, list_id, kind, description_enc, rank, handout_meta_enc, alias_enc, created_at, updated_at) values(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			bid, listID, kind, descEnc, req.Rank, metaEnc, aliasEnc, rfc3339(now), rfc3339(now))
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

type patchCardRequest struct {
	DescEnc        *string `json:"description_enc"`
	Rank           *string `json:"rank"`
	ListID         *int64  `json:"list_id"`
	Kind           *string `json:"kind"`             // optional kind change (feature: change card type after creation)
	DescEventEnc   *string `json:"desc_event_enc"`   // optional desc_edit timeline payload
	HandoutMetaEnc *string `json:"handout_meta_enc"` // optional handout-gen settings; "" clears (sets NULL)
	AliasEnc       *string `json:"alias_enc"`        // optional short card label; "" clears (sets NULL)
}

// optBlob maps an optional base64 field to nullable blob bytes: nil pointer or
// empty string → NULL; otherwise the decoded envelope.
func optBlob(b64v *string) ([]byte, error) {
	if b64v == nil || *b64v == "" {
		return nil, nil
	}
	return unb64(*b64v)
}

func (s *server) handlePatchCard(w http.ResponseWriter, r *http.Request) {
	uid, cardID, bid, ok := s.requireChildAccess(w, r, boardOfCard)
	if !ok {
		return
	}
	var req patchCardRequest
	if !readJSON(w, r, &req) {
		return
	}
	err := s.withWriteTx(r.Context(), "patch-card", func(ctx context.Context, tx *sql.Tx) error {
		now := rfc3339(time.Now())
		if req.DescEnc != nil {
			descEnc, err := unb64(*req.DescEnc)
			if err != nil {
				return errBadRequest("invalid description_enc")
			}
			if _, err := tx.ExecContext(ctx, `update cards set description_enc = ?, updated_at = ? where id = ?`, descEnc, now, cardID); err != nil {
				return err
			}
			if req.DescEventEnc != nil {
				if err := appendEvent(ctx, tx, bid, cardID, "desc_edit", uid, *req.DescEventEnc); err != nil {
					return err
				}
			}
		}
		if req.HandoutMetaEnc != nil {
			metaEnc, err := optBlob(req.HandoutMetaEnc)
			if err != nil {
				return errBadRequest("invalid handout_meta_enc")
			}
			if _, err := tx.ExecContext(ctx, `update cards set handout_meta_enc = ?, updated_at = ? where id = ?`, metaEnc, now, cardID); err != nil {
				return err
			}
		}
		if req.AliasEnc != nil {
			aliasEnc, err := optBlob(req.AliasEnc)
			if err != nil {
				return errBadRequest("invalid alias_enc")
			}
			if _, err := tx.ExecContext(ctx, `update cards set alias_enc = ?, updated_at = ? where id = ?`, aliasEnc, now, cardID); err != nil {
				return err
			}
		}
		if req.Rank != nil {
			if _, err := tx.ExecContext(ctx, `update cards set rank = ?, updated_at = ? where id = ?`, *req.Rank, now, cardID); err != nil {
				return err
			}
		}
		if req.Kind != nil {
			if !validCardKind(*req.Kind) {
				return errBadRequest("bad card kind")
			}
			if _, err := tx.ExecContext(ctx, `update cards set kind = ?, updated_at = ? where id = ?`, *req.Kind, now, cardID); err != nil {
				return err
			}
		}
		if req.ListID != nil {
			// Verify the target list is in the same board.
			var targetBoard int64
			if err := tx.QueryRowContext(ctx, `select board_id from lists where id = ? and deleted_at is null`, *req.ListID).Scan(&targetBoard); err != nil {
				return errBadRequest("целевой список не найден")
			}
			if targetBoard != bid {
				return errBadRequest("нельзя перемещать карточку между досками здесь")
			}
			if _, err := tx.ExecContext(ctx, `update cards set list_id = ?, updated_at = ? where id = ?`, *req.ListID, now, cardID); err != nil {
				return err
			}
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleDeleteCard(w http.ResponseWriter, r *http.Request) {
	_, cardID, _, ok := s.requireChildAccess(w, r, boardOfCard)
	if !ok {
		return
	}
	err := s.withWriteTx(r.Context(), "delete-card", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update cards set deleted_at = ? where id = ?`, rfc3339(time.Now()), cardID)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- labels ----

type createLabelRequest struct {
	NameEnc  string `json:"name_enc"`
	ColorEnc string `json:"color_enc"`
	Kind     string `json:"kind"`
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
	kind := req.Kind
	if kind == "" {
		kind = "normal"
	}
	now := time.Now()
	var id int64
	err := s.withWriteTx(r.Context(), "create-label", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `insert into labels(board_id, name_enc, color_enc, kind, created_at) values(?, ?, ?, ?, ?)`,
			bid, nameEnc, colorEnc, kind, rfc3339(now))
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
	u, authed := s.requireUser(w, r)
	if !authed {
		return
	}
	labelID, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	var bid int64
	if err := s.db.QueryRowContext(r.Context(), `select board_id from labels where id = ? and deleted_at is null`, labelID).Scan(&bid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpError(w, http.StatusNotFound, "метка не найдена")
		} else {
			handleErr(w, err)
		}
		return
	}
	if _, err := boardRole(r.Context(), s.db, bid, u.UserID); handleErr(w, err) {
		return
	}
	var req patchLabelRequest
	if !readJSON(w, r, &req) {
		return
	}
	err := s.withWriteTx(r.Context(), "patch-label", func(ctx context.Context, tx *sql.Tx) error {
		if req.NameEnc != nil {
			nameEnc, err := unb64(*req.NameEnc)
			if err != nil {
				return errBadRequest("invalid name_enc")
			}
			if _, err := tx.ExecContext(ctx, `update labels set name_enc = ? where id = ?`, nameEnc, labelID); err != nil {
				return err
			}
		}
		if req.ColorEnc != nil {
			colorEnc, err := unb64(*req.ColorEnc)
			if err != nil {
				return errBadRequest("invalid color_enc")
			}
			if _, err := tx.ExecContext(ctx, `update labels set color_enc = ? where id = ?`, colorEnc, labelID); err != nil {
				return err
			}
		}
		return nil
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
	var bid int64
	if err := s.db.QueryRowContext(r.Context(), `select board_id from labels where id = ?`, labelID).Scan(&bid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusNoContent)
		} else {
			handleErr(w, err)
		}
		return
	}
	if _, err := boardRole(r.Context(), s.db, bid, u.UserID); handleErr(w, err) {
		return
	}
	err := s.withWriteTx(r.Context(), "delete-label", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update labels set deleted_at = ? where id = ?`, rfc3339(time.Now()), labelID)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type setCardLabelsRequest struct {
	LabelIDs []int64      `json:"label_ids"`
	Events   []eventInput `json:"events"` // optional label_add/label_remove timeline events
}

type eventInput struct {
	Type       string `json:"type"`
	PayloadEnc string `json:"payload_enc"`
}

func (s *server) handleSetCardLabels(w http.ResponseWriter, r *http.Request) {
	uid, cardID, bid, ok := s.requireChildAccess(w, r, boardOfCard)
	if !ok {
		return
	}
	var req setCardLabelsRequest
	if !readJSON(w, r, &req) {
		return
	}
	err := s.withWriteTx(r.Context(), "set-card-labels", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `delete from card_labels where card_id = ?`, cardID); err != nil {
			return err
		}
		for _, lid := range req.LabelIDs {
			// Ensure the label belongs to this board.
			var lb int64
			if err := tx.QueryRowContext(ctx, `select board_id from labels where id = ? and deleted_at is null`, lid).Scan(&lb); err != nil {
				return errBadRequest("метка не найдена")
			}
			if lb != bid {
				return errBadRequest("метка с другой доски")
			}
			if _, err := tx.ExecContext(ctx, `insert into card_labels(card_id, label_id) values(?, ?)`, cardID, lid); err != nil {
				return err
			}
		}
		for _, ev := range req.Events {
			if ev.Type != "label_add" && ev.Type != "label_remove" {
				return errBadRequest("bad label event type")
			}
			if err := appendEvent(ctx, tx, bid, cardID, ev.Type, uid, ev.PayloadEnc); err != nil {
				return err
			}
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- timeline ----

type timelineEventDTO struct {
	ID         int64   `json:"id"`
	Type       string  `json:"type"`
	AuthorID   *int64  `json:"author_user_id"`
	CreatedAt  string  `json:"created_at"`
	EditedAt   *string `json:"edited_at,omitempty"`
	IsExcerpt  bool    `json:"is_excerpt"`
	ReplyToID  *int64  `json:"reply_to_id,omitempty"`
	ReplyCount int     `json:"reply_count"`
	// Deleted marks a tombstone: a comment whose text is gone but which is still
	// rendered because live replies hang off it. PayloadEnc is empty for these.
	Deleted    bool   `json:"deleted,omitempty"`
	PayloadEnc string `json:"payload_enc"`
}

func (s *server) handleGetTimeline(w http.ResponseWriter, r *http.Request) {
	_, cardID, _, ok := s.requireChildAccess(w, r, boardOfCard)
	if !ok {
		return
	}
	// A deleted comment is normally gone from the лента, but one that still
	// anchors live replies is returned as a tombstone (deleted = 1, empty
	// payload) so the thread beneath it stays reachable instead of orphaned.
	rows, err := s.db.QueryContext(r.Context(), `
select e.id, e.type, e.author_user_id, e.created_at, e.edited_at, e.is_excerpt,
       e.reply_to_id, e.deleted_at is not null,
       (select count(*) from timeline_events r
          where r.reply_to_id = e.id and r.deleted_at is null),
       e.payload_enc
from timeline_events e
where e.card_id = ?
  and (e.deleted_at is null
       or exists (select 1 from timeline_events r
                    where r.reply_to_id = e.id and r.deleted_at is null))
order by e.id`, cardID)
	if handleErr(w, err) {
		return
	}
	defer rows.Close()
	out := []timelineEventDTO{}
	for rows.Next() {
		var e timelineEventDTO
		var author, replyTo sql.NullInt64
		var edited sql.NullString
		var excerpt, deleted int
		var payload []byte
		if err := rows.Scan(&e.ID, &e.Type, &author, &e.CreatedAt, &edited, &excerpt,
			&replyTo, &deleted, &e.ReplyCount, &payload); handleErr(w, err) {
			return
		}
		if author.Valid {
			e.AuthorID = &author.Int64
		}
		if edited.Valid {
			e.EditedAt = &edited.String
		}
		if replyTo.Valid {
			e.ReplyToID = &replyTo.Int64
		}
		e.IsExcerpt = excerpt != 0
		e.Deleted = deleted != 0
		e.PayloadEnc = b64(payload)
		out = append(out, e)
	}
	if err := rows.Err(); handleErr(w, err) {
		return
	}
	writeJSON(w, out)
}

type addCommentRequest struct {
	PayloadEnc string `json:"payload_enc"`
	ReplyToID  *int64 `json:"reply_to_id"`
}

func (s *server) handleAddComment(w http.ResponseWriter, r *http.Request) {
	uid, cardID, bid, ok := s.requireChildAccess(w, r, boardOfCard)
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
	var replyTo any
	if req.ReplyToID != nil {
		root, err := threadRoot(r.Context(), s.db, *req.ReplyToID, cardID)
		if handleErr(w, err) {
			return
		}
		replyTo = root
	}
	err := s.withWriteTx(r.Context(), "add-comment", func(ctx context.Context, tx *sql.Tx) error {
		payload, err := unb64(req.PayloadEnc)
		if err != nil {
			return errBadRequest("invalid payload_enc")
		}
		_, err = tx.ExecContext(ctx, `
insert into timeline_events(board_id, card_id, type, author_user_id, created_at, payload_enc, reply_to_id)
values(?, ?, 'comment', ?, ?, ?, ?)`, bid, cardID, uid, rfc3339(time.Now()), payload, replyTo)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// threadRoot resolves the comment a reply should hang off. Threads are one level
// deep: replying to a reply attaches to that reply's root, so a thread is always
// a flat run under a single parent. The target must be a comment on the SAME
// card — otherwise a reply could be smuggled onto another board's discussion.
// A tombstoned parent is still a valid target; its thread outlives its text.
func threadRoot(ctx context.Context, q querier, id, cardID int64) (int64, error) {
	var root sql.NullInt64
	var owner int64
	err := q.QueryRowContext(ctx, `
select card_id, coalesce(reply_to_id, id) from timeline_events where id = ? and type = 'comment'`, id).
		Scan(&owner, &root)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errNotFound("комментарий не найден")
	}
	if err != nil {
		return 0, err
	}
	if owner != cardID {
		return 0, errBadRequest("комментарий с другой карточки")
	}
	return root.Int64, nil
}

type patchCommentRequest struct {
	PayloadEnc *string `json:"payload_enc"`
	IsExcerpt  *bool   `json:"is_excerpt"`
}

// handlePatchComment edits a comment's text and/or flips its «выписка» flag.
// The two fields carry different permissions: rewriting what someone said is
// the author's business alone, while marking a comment as an excerpt is
// curation any board member may do (the same trust level as adding one).
func (s *server) handlePatchComment(w http.ResponseWriter, r *http.Request) {
	uid, evID, _, _, author, ok := s.requireComment(w, r)
	if !ok {
		return
	}
	var req patchCommentRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.PayloadEnc != nil && (author == nil || *author != uid) {
		httpError(w, http.StatusForbidden, "редактировать может только автор")
		return
	}
	err := s.withWriteTx(r.Context(), "patch-comment", func(ctx context.Context, tx *sql.Tx) error {
		if req.IsExcerpt != nil {
			flag := 0
			if *req.IsExcerpt {
				flag = 1
			}
			if _, err := tx.ExecContext(ctx, `update timeline_events set is_excerpt = ? where id = ?`, flag, evID); err != nil {
				return err
			}
		}
		if req.PayloadEnc != nil {
			payload, err := unb64(*req.PayloadEnc)
			if err != nil || len(payload) == 0 {
				return errBadRequest("invalid payload_enc")
			}
			if _, err := tx.ExecContext(ctx, `
update timeline_events set payload_enc = ?, edited_at = ? where id = ?`, payload, rfc3339(time.Now()), evID); err != nil {
				return err
			}
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteComment tombstones a comment (author only). The row survives so
// the id stays taken — read watermarks are ids, and reusing one would silently
// mark later comments read — and so a thread hanging off it stays anchored. The
// TEXT is scrubbed, not merely hidden: a tombstone with replies is still sent to
// clients, and delete has to mean the words are gone.
func (s *server) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	uid, evID, _, _, author, ok := s.requireComment(w, r)
	if !ok {
		return
	}
	if author == nil || *author != uid {
		httpError(w, http.StatusForbidden, "удалить может только автор")
		return
	}
	err := s.withWriteTx(r.Context(), "delete-comment", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
update timeline_events set deleted_at = ?, payload_enc = x'', is_excerpt = 0 where id = ?`, rfc3339(time.Now()), evID)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireComment resolves {id} to a live comment event the caller may see,
// returning the caller's uid plus the event's board, card and author.
func (s *server) requireComment(w http.ResponseWriter, r *http.Request) (uid, evID, bid, cardID int64, author *int64, ok bool) {
	u, okU := s.requireUser(w, r)
	if !okU {
		return
	}
	id, okP := pathInt(w, r, "id")
	if !okP {
		return
	}
	var a sql.NullInt64
	err := s.db.QueryRowContext(r.Context(), `
select board_id, card_id, author_user_id from timeline_events
where id = ? and type = 'comment' and deleted_at is null`, id).Scan(&bid, &cardID, &a)
	if errors.Is(err, sql.ErrNoRows) {
		httpError(w, http.StatusNotFound, "комментарий не найден")
		return
	}
	if handleErr(w, err) {
		return
	}
	if _, err := boardRole(r.Context(), s.db, bid, u.UserID); handleErr(w, err) {
		return
	}
	if a.Valid {
		author = &a.Int64
	}
	return u.UserID, id, bid, cardID, author, true
}

type importCommentsRequest struct {
	Comments []importedComment `json:"comments"`
}

type importedComment struct {
	// SrcID / ReplyToSrcID are the SOURCE card's event ids, used only to rebuild
	// threading: the copy gets fresh ids, so a reply's parent is resolved through
	// a src→new map as the batch is inserted (oldest first, so a parent is always
	// already mapped by the time its reply arrives).
	SrcID        int64  `json:"src_id"`
	ReplyToSrcID *int64 `json:"reply_to_src_id"`
	AuthorUserID *int64 `json:"author_user_id"`
	CreatedAt    string `json:"created_at"`
	IsExcerpt    bool   `json:"is_excerpt"`
	PayloadEnc   string `json:"payload_enc"`
}

// handleImportComments bulk-inserts comment events while preserving their
// original author + timestamp — used by the copy/move path so a duplicated card
// keeps its discussion intact instead of re-stamping every comment to the copier
// and "now". Authorship here is advisory display metadata (the same trust model
// as the rest of the board: any editor can already write arbitrary encrypted
// content); author_user_id, when present, must reference a real user (FK).
func (s *server) handleImportComments(w http.ResponseWriter, r *http.Request) {
	_, cardID, bid, ok := s.requireChildAccess(w, r, boardOfCard)
	if !ok {
		return
	}
	var req importCommentsRequest
	if !readJSON(w, r, &req) {
		return
	}
	err := s.withWriteTx(r.Context(), "import-comments", func(ctx context.Context, tx *sql.Tx) error {
		newID := make(map[int64]int64, len(req.Comments))
		for _, c := range req.Comments {
			payload, err := unb64(c.PayloadEnc)
			if err != nil {
				return errBadRequest("invalid payload_enc")
			}
			created := c.CreatedAt
			if _, perr := time.Parse(time.RFC3339, created); perr != nil {
				created = rfc3339(time.Now()) // fall back on a missing/garbled timestamp
			}
			var author any
			if c.AuthorUserID != nil {
				author = *c.AuthorUserID
			}
			// An unresolvable parent (out-of-order or absent from the batch) drops
			// the reply to top level rather than failing the whole copy.
			var replyTo any
			if c.ReplyToSrcID != nil {
				if mapped, ok := newID[*c.ReplyToSrcID]; ok {
					replyTo = mapped
				}
			}
			res, err := tx.ExecContext(ctx, `
insert into timeline_events(board_id, card_id, type, author_user_id, created_at, is_excerpt, payload_enc, reply_to_id)
values(?, ?, 'comment', ?, ?, ?, ?, ?)`, bid, cardID, author, created, boolInt(c.IsExcerpt), payload, replyTo)
			if err != nil {
				return err
			}
			id, err := res.LastInsertId()
			if err != nil {
				return err
			}
			if c.SrcID != 0 {
				newID[c.SrcID] = id
			}
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// appendEvent inserts a timeline event with a base64 payload envelope.
func appendEvent(ctx context.Context, tx *sql.Tx, boardID, cardID int64, typ string, authorID int64, payloadB64 string) error {
	payload, err := unb64(payloadB64)
	if err != nil {
		return errBadRequest("invalid payload_enc")
	}
	_, err = tx.ExecContext(ctx, `
insert into timeline_events(board_id, card_id, type, author_user_id, created_at, payload_enc)
values(?, ?, ?, ?, ?, ?)`, boardID, cardID, typ, authorID, rfc3339(time.Now()), payload)
	return err
}
