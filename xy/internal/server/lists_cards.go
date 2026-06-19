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
}

type cardDTO struct {
	ID      int64  `json:"id"`
	ListID  int64  `json:"list_id"`
	Kind    string `json:"kind"`
	DescEnc string `json:"description_enc"`
	Rank    string `json:"rank"`
}

type labelDTO struct {
	ID       int64  `json:"id"`
	NameEnc  string `json:"name_enc"`
	ColorEnc string `json:"color_enc"`
	Kind     string `json:"kind"`
}

func scanLists(ctx context.Context, q querier, boardID int64) ([]listDTO, error) {
	rows, err := q.QueryContext(ctx, `
select id, type, title_enc, rank from lists where board_id = ? and deleted_at is null order by rank`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []listDTO{}
	for rows.Next() {
		var l listDTO
		var titleEnc []byte
		if err := rows.Scan(&l.ID, &l.Type, &titleEnc, &l.Rank); err != nil {
			return nil, err
		}
		l.TitleEnc = b64(titleEnc)
		out = append(out, l)
	}
	return out, rows.Err()
}

func scanCards(ctx context.Context, q querier, boardID int64) ([]cardDTO, error) {
	rows, err := q.QueryContext(ctx, `
select id, list_id, kind, description_enc, rank from cards where board_id = ? and deleted_at is null order by rank`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cardDTO{}
	for rows.Next() {
		var c cardDTO
		var descEnc []byte
		if err := rows.Scan(&c.ID, &c.ListID, &c.Kind, &descEnc, &c.Rank); err != nil {
			return nil, err
		}
		c.DescEnc = b64(descEnc)
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

// ---- cards ----

type createCardRequest struct {
	DescEnc string `json:"description_enc"`
	Rank    string `json:"rank"`
	Kind    string `json:"kind"`
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
	now := time.Now()
	var id int64
	err = s.withWriteTx(r.Context(), "create-card", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
insert into cards(board_id, list_id, kind, description_enc, rank, created_at, updated_at) values(?, ?, ?, ?, ?, ?, ?)`,
			bid, listID, kind, descEnc, req.Rank, rfc3339(now), rfc3339(now))
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
	DescEnc      *string `json:"description_enc"`
	Rank         *string `json:"rank"`
	ListID       *int64  `json:"list_id"`
	DescEventEnc *string `json:"desc_event_enc"` // optional desc_edit timeline payload
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
		if req.Rank != nil {
			if _, err := tx.ExecContext(ctx, `update cards set rank = ?, updated_at = ? where id = ?`, *req.Rank, now, cardID); err != nil {
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
	ID         int64  `json:"id"`
	Type       string `json:"type"`
	AuthorID   *int64 `json:"author_user_id"`
	CreatedAt  string `json:"created_at"`
	PayloadEnc string `json:"payload_enc"`
}

func (s *server) handleGetTimeline(w http.ResponseWriter, r *http.Request) {
	_, cardID, _, ok := s.requireChildAccess(w, r, boardOfCard)
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
select id, type, author_user_id, created_at, payload_enc from timeline_events where card_id = ? order by id`, cardID)
	if handleErr(w, err) {
		return
	}
	defer rows.Close()
	out := []timelineEventDTO{}
	for rows.Next() {
		var e timelineEventDTO
		var author sql.NullInt64
		var payload []byte
		if err := rows.Scan(&e.ID, &e.Type, &author, &e.CreatedAt, &payload); handleErr(w, err) {
			return
		}
		if author.Valid {
			e.AuthorID = &author.Int64
		}
		e.PayloadEnc = b64(payload)
		out = append(out, e)
	}
	writeJSON(w, out)
}

type addCommentRequest struct {
	PayloadEnc string `json:"payload_enc"`
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
	err := s.withWriteTx(r.Context(), "add-comment", func(ctx context.Context, tx *sql.Tx) error {
		return appendEvent(ctx, tx, bid, cardID, "comment", uid, req.PayloadEnc)
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
