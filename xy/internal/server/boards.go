package server

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"time"
)

// querier is satisfied by *sql.DB, *sql.Conn, and *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func unb64(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

// pathInt reads an int64 path value, writing 400 on failure.
func pathInt(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	v, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "bad "+name)
		return 0, false
	}
	return v, true
}

// boardRole returns the caller's role on a board, or an appError (403/404).
func boardRole(ctx context.Context, q querier, boardID, userID int64) (string, error) {
	var deleted sql.NullString
	if err := q.QueryRowContext(ctx, `select deleted_at from boards where id = ?`, boardID).Scan(&deleted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errNotFound("доска не найдена")
		}
		return "", err
	}
	if deleted.Valid {
		return "", errNotFound("доска удалена")
	}
	var role string
	err := q.QueryRowContext(ctx, `select role from board_members where board_id = ? and user_id = ?`, boardID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errForbidden("нет доступа к доске")
	}
	if err != nil {
		return "", err
	}
	return role, nil
}

// requireBoard resolves the user + their role on the board path param, or writes
// an error response and returns ok=false.
func (s *server) requireBoard(w http.ResponseWriter, r *http.Request, paramName string) (userID, boardID int64, role string, ok bool) {
	u, authed := s.requireUser(w, r)
	if !authed {
		return 0, 0, "", false
	}
	bid, okp := pathInt(w, r, paramName)
	if !okp {
		return 0, 0, "", false
	}
	role, err := boardRole(r.Context(), s.db, bid, u.UserID)
	if handleErr(w, err) {
		return 0, 0, "", false
	}
	return u.UserID, bid, role, true
}

// ---- wire types ----

type boardSummary struct {
	ID        int64  `json:"id"`
	NameEnc   string `json:"name_enc"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func (s *server) handleListBoards(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
select b.id, b.name_enc, m.role, b.created_at, b.updated_at
from boards b join board_members m on m.board_id = b.id
where m.user_id = ? and b.deleted_at is null
order by b.updated_at desc`, u.UserID)
	if handleErr(w, err) {
		return
	}
	defer rows.Close()
	out := []boardSummary{}
	for rows.Next() {
		var b boardSummary
		var nameEnc []byte
		if err := rows.Scan(&b.ID, &nameEnc, &b.Role, &b.CreatedAt, &b.UpdatedAt); handleErr(w, err) {
			return
		}
		b.NameEnc = b64(nameEnc)
		out = append(out, b)
	}
	writeJSON(w, out)
}

type createBoardRequest struct {
	NameEnc     string `json:"name_enc"`
	KDFSalt     string `json:"kdf_salt"`
	KDFParams   string `json:"kdf_params"`
	WrappedKey  string `json:"wrapped_key"`
	VerifyToken string `json:"verify_token"`
}

func (s *server) handleCreateBoard(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req createBoardRequest
	if !readJSON(w, r, &req) {
		return
	}
	nameEnc, err1 := unb64(req.NameEnc)
	salt, err2 := unb64(req.KDFSalt)
	wrapped, err3 := unb64(req.WrappedKey)
	verify, err4 := unb64(req.VerifyToken)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || req.KDFParams == "" {
		httpError(w, http.StatusBadRequest, "invalid board crypto fields")
		return
	}
	now := time.Now()
	var boardID int64
	err := s.withWriteTx(r.Context(), "create-board", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
insert into boards(owner_user_id, name_enc, kdf_salt, kdf_params, wrapped_key, verify_token, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, ?)`,
			u.UserID, nameEnc, salt, req.KDFParams, wrapped, verify, rfc3339(now), rfc3339(now))
		if err != nil {
			return err
		}
		boardID, err = res.LastInsertId()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `insert into board_members(board_id, user_id, role) values(?, ?, 'owner')`, boardID, u.UserID)
		return err
	})
	if handleErr(w, err) {
		return
	}
	writeJSON(w, map[string]any{"id": boardID})
}

// boardSnapshot is the single-fetch board payload the UI bootstraps from.
type boardSnapshot struct {
	ID         int64              `json:"id"`
	NameEnc    string             `json:"name_enc"`
	Role       string             `json:"role"`
	Lists      []listDTO          `json:"lists"`
	Cards      []cardDTO          `json:"cards"`
	Labels     []labelDTO         `json:"labels"`
	CardLabels map[string][]int64 `json:"card_labels"`
}

func (s *server) handleGetBoard(w http.ResponseWriter, r *http.Request) {
	uid, bid, role, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	_ = uid
	ctx := r.Context()
	snap := boardSnapshot{ID: bid, Role: role, Lists: []listDTO{}, Cards: []cardDTO{}, Labels: []labelDTO{}, CardLabels: map[string][]int64{}}

	var nameEnc []byte
	if err := s.db.QueryRowContext(ctx, `select name_enc from boards where id = ?`, bid).Scan(&nameEnc); handleErr(w, err) {
		return
	}
	snap.NameEnc = b64(nameEnc)

	lists, err := scanLists(ctx, s.db, bid)
	if handleErr(w, err) {
		return
	}
	snap.Lists = lists

	cards, err := scanCards(ctx, s.db, bid)
	if handleErr(w, err) {
		return
	}
	snap.Cards = cards

	labels, err := scanLabels(ctx, s.db, bid)
	if handleErr(w, err) {
		return
	}
	snap.Labels = labels

	clRows, err := s.db.QueryContext(ctx, `
select cl.card_id, cl.label_id
from card_labels cl join cards c on c.id = cl.card_id
where c.board_id = ? and c.deleted_at is null`, bid)
	if handleErr(w, err) {
		return
	}
	defer clRows.Close()
	for clRows.Next() {
		var cardID, labelID int64
		if err := clRows.Scan(&cardID, &labelID); handleErr(w, err) {
			return
		}
		key := strconv.FormatInt(cardID, 10)
		snap.CardLabels[key] = append(snap.CardLabels[key], labelID)
	}
	writeJSON(w, snap)
}

type patchBoardRequest struct {
	NameEnc *string `json:"name_enc"`
}

func (s *server) handlePatchBoard(w http.ResponseWriter, r *http.Request) {
	_, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	var req patchBoardRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.NameEnc == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	nameEnc, err := unb64(*req.NameEnc)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid name_enc")
		return
	}
	err = s.withWriteTx(r.Context(), "patch-board", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update boards set name_enc = ?, updated_at = ? where id = ?`,
			nameEnc, rfc3339(time.Now()), bid)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleDeleteBoard(w http.ResponseWriter, r *http.Request) {
	_, bid, role, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	if role != "owner" {
		httpError(w, http.StatusForbidden, "только владелец может удалить доску")
		return
	}
	err := s.withWriteTx(r.Context(), "delete-board", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update boards set deleted_at = ? where id = ?`, rfc3339(time.Now()), bid)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- keymeta (passphrase wrapping) ----

type keymetaResponse struct {
	KDFSalt     string `json:"kdf_salt"`
	KDFParams   string `json:"kdf_params"`
	WrappedKey  string `json:"wrapped_key"`
	VerifyToken string `json:"verify_token"`
}

func (s *server) handleGetKeymeta(w http.ResponseWriter, r *http.Request) {
	_, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	var salt, wrapped, verify []byte
	var params string
	err := s.db.QueryRowContext(r.Context(), `select kdf_salt, kdf_params, wrapped_key, verify_token from boards where id = ?`, bid).
		Scan(&salt, &params, &wrapped, &verify)
	if handleErr(w, err) {
		return
	}
	writeJSON(w, keymetaResponse{KDFSalt: b64(salt), KDFParams: params, WrappedKey: b64(wrapped), VerifyToken: b64(verify)})
}

type putKeymetaRequest struct {
	KDFSalt    string `json:"kdf_salt"`
	KDFParams  string `json:"kdf_params"`
	WrappedKey string `json:"wrapped_key"`
}

// handlePutKeymeta re-wraps the data key after a passphrase change. The same DK
// is re-wrapped under a new KEK; board data is never re-encrypted, so the verify
// token stays valid.
func (s *server) handlePutKeymeta(w http.ResponseWriter, r *http.Request) {
	_, bid, role, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	if role != "owner" {
		httpError(w, http.StatusForbidden, "только владелец может менять пароль доски")
		return
	}
	var req putKeymetaRequest
	if !readJSON(w, r, &req) {
		return
	}
	salt, err1 := unb64(req.KDFSalt)
	wrapped, err2 := unb64(req.WrappedKey)
	if err1 != nil || err2 != nil || req.KDFParams == "" {
		httpError(w, http.StatusBadRequest, "invalid keymeta")
		return
	}
	err := s.withWriteTx(r.Context(), "put-keymeta", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update boards set kdf_salt = ?, kdf_params = ?, wrapped_key = ?, updated_at = ? where id = ?`,
			salt, req.KDFParams, wrapped, rfc3339(time.Now()), bid)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- members ----

type memberDTO struct {
	UserID   int64   `json:"user_id"`
	Role     string  `json:"role"`
	Username *string `json:"username"`
}

func (s *server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	_, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
select m.user_id, m.role, coalesce(nullif(u.username, ''), u.telegram_username)
from board_members m join users u on u.id = m.user_id where m.board_id = ?
order by case m.role when 'owner' then 0 else 1 end, u.username`, bid)
	if handleErr(w, err) {
		return
	}
	defer rows.Close()
	out := []memberDTO{}
	for rows.Next() {
		var m memberDTO
		var uname sql.NullString
		if err := rows.Scan(&m.UserID, &m.Role, &uname); handleErr(w, err) {
			return
		}
		if uname.Valid {
			m.Username = &uname.String
		}
		out = append(out, m)
	}
	writeJSON(w, out)
}

type addMemberRequest struct {
	Username string `json:"username"`
}

func (s *server) handleAddMember(w http.ResponseWriter, r *http.Request) {
	_, bid, role, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	if role != "owner" {
		httpError(w, http.StatusForbidden, "только владелец может добавлять участников")
		return
	}
	var req addMemberRequest
	if !readJSON(w, r, &req) {
		return
	}
	err := s.withWriteTx(r.Context(), "add-member", func(ctx context.Context, tx *sql.Tx) error {
		var memberID int64
		if err := tx.QueryRowContext(ctx, `select id from users where username = ?`, req.Username).Scan(&memberID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errBadRequest("пользователь не найден")
			}
			return err
		}
		_, err := tx.ExecContext(ctx, `
insert into board_members(board_id, user_id, role) values(?, ?, 'editor')
on conflict(board_id, user_id) do nothing`, bid, memberID)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	_, bid, role, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	if role != "owner" {
		httpError(w, http.StatusForbidden, "только владелец может удалять участников")
		return
	}
	memberID, ok := pathInt(w, r, "userId")
	if !ok {
		return
	}
	err := s.withWriteTx(r.Context(), "remove-member", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `delete from board_members where board_id = ? and user_id = ? and role <> 'owner'`, bid, memberID)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
