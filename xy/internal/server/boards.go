package server

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
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
	ID      int64  `json:"id"`
	Name    string `json:"name"`     // plaintext (schema_version 2); "" for legacy
	NameEnc string `json:"name_enc"` // legacy ciphertext (schema_version 1 fallback)
	// SchemaVersion: 1 = name still encrypted in name_enc; 2 = name is plaintext.
	SchemaVersion int     `json:"schema_version"`
	Role          string  `json:"role"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
	LastVisitedAt *string `json:"last_visited_at"` // nil = never visited on record
	Unread        bool    `json:"unread"`          // any card has an unread change by another member
}

func (s *server) handleListBoards(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	// Order by the caller's own last visit (most recent first; never-visited
	// boards fall back to update time). `unread` aggregates the per-card unread
	// predicate (see the board snapshot / activity feed) across the whole board.
	rows, err := s.db.QueryContext(r.Context(), `
select b.id, b.name, b.name_enc, b.schema_version, m.role, b.created_at, b.updated_at, m.last_visited_at,
  exists(
    select 1 from timeline_events e
    join cards c on c.id = e.card_id and c.deleted_at is null
    left join card_reads cr on cr.card_id = e.card_id and cr.user_id = m.user_id
    where e.board_id = b.id and e.author_user_id is not null and e.author_user_id <> m.user_id
      and ((e.type =  'comment' and e.id > coalesce(cr.comment_read_id,0))
        or (e.type <> 'comment' and e.id > coalesce(cr.content_read_id,0)))
  ) as unread
from boards b join board_members m on m.board_id = b.id
where m.user_id = ? and b.deleted_at is null
order by m.last_visited_at is null, m.last_visited_at desc, b.updated_at desc`, u.UserID)
	if handleErr(w, err) {
		return
	}
	defer rows.Close()
	out := []boardSummary{}
	for rows.Next() {
		var b boardSummary
		var name sql.NullString
		var nameEnc []byte
		var lastVisited sql.NullString
		var unread int
		if err := rows.Scan(&b.ID, &name, &nameEnc, &b.SchemaVersion, &b.Role, &b.CreatedAt, &b.UpdatedAt, &lastVisited, &unread); handleErr(w, err) {
			return
		}
		b.Name = name.String
		b.NameEnc = b64(nameEnc)
		if lastVisited.Valid {
			b.LastVisitedAt = &lastVisited.String
		}
		b.Unread = unread == 1
		out = append(out, b)
	}
	writeJSON(w, out)
}

// handleBoardVisit stamps the caller's last-visit time for a board (used to
// order the board list). Fire-and-forget from the client on board open;
// online-only best-effort, like read-marking.
func (s *server) handleBoardVisit(w http.ResponseWriter, r *http.Request) {
	uid, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	err := s.withWriteTx(r.Context(), "board-visit", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update board_members set last_visited_at = ? where board_id = ? and user_id = ?`,
			rfc3339(time.Now()), bid, uid)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type createBoardRequest struct {
	Name        string `json:"name"` // plaintext; the board's data (lists/cards/…) is still encrypted
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
	salt, err2 := unb64(req.KDFSalt)
	wrapped, err3 := unb64(req.WrappedKey)
	verify, err4 := unb64(req.VerifyToken)
	if strings.TrimSpace(req.Name) == "" || err2 != nil || err3 != nil || err4 != nil || req.KDFParams == "" {
		httpError(w, http.StatusBadRequest, "invalid board fields")
		return
	}
	now := time.Now()
	var boardID int64
	err := s.withWriteTx(r.Context(), "create-board", func(ctx context.Context, tx *sql.Tx) error {
		// New boards are born at schema_version 2 (plaintext name). name_enc is
		// NOT NULL for legacy reasons, so store an empty blob until it's dropped.
		res, err := tx.ExecContext(ctx, `
insert into boards(owner_user_id, name, name_enc, schema_version, kdf_salt, kdf_params, wrapped_key, verify_token, created_at, updated_at)
values(?, ?, x'', 2, ?, ?, ?, ?, ?, ?)`,
			u.UserID, req.Name, salt, req.KDFParams, wrapped, verify, rfc3339(now), rfc3339(now))
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
	ID      int64  `json:"id"`
	Name    string `json:"name"`     // plaintext (schema_version 2); "" for legacy
	NameEnc string `json:"name_enc"` // legacy ciphertext (schema_version 1 fallback)
	// SchemaVersion: 1 = name still encrypted in name_enc; 2 = name is plaintext.
	SchemaVersion int            `json:"schema_version"`
	Role          string         `json:"role"`
	Lists         []listDTO      `json:"lists"`
	Groups        []groupDTO     `json:"groups"`
	Cards         []cardDTO      `json:"cards"`
	Labels        []labelDTO     `json:"labels"`
	Sessions      []sessionDTO   `json:"sessions"`
	CardLabels    []cardLabelDTO `json:"card_labels"`
	// CardSessions is the Playings — one row per (card, session), the same shape
	// as CardLabels because both mirror their table. What «Видели» reads.
	CardSessions []cardSessionDTO `json:"card_sessions"`
	// TourTesters is each tour's Declaration: which sessions its «Вопросы
	// тестировали» line names. A tour with none falls back to the custom.
	TourTesters []tourTesterDTO      `json:"tour_testers"`
	Unread      map[string]unreadDTO `json:"unread"`
	// Sizes is the CALLER's display layout ({boardW,listW,cardLines}) — a per-user,
	// all-boards preference (users.sizes, plaintext JSON; see migrateV9), delivered
	// here alongside the snapshot's other caller-specific fields (role, unread) so
	// the board renders at the user's sizes without a second fetch. Omitted when
	// never set — the client then uses its defaults. Written via POST /api/auth/sizes.
	Sizes json.RawMessage `json:"sizes,omitempty"`
	// DefaultAuthor is the caller's pre-fill for new question cards
	// (users.default_author), delivered like Sizes so the card editor works
	// offline from the cached snapshot.
	DefaultAuthor string `json:"default_author,omitempty"`
	// CardTitle is the caller's card-preview mode ("question" / "answer",
	// users.card_title), delivered like Sizes so the board renders previews the
	// reader's way straight from the cached snapshot. A card's alias wins over it.
	CardTitle string `json:"card_title,omitempty"`
	// Timezone is the caller's IANA zone (users.timezone): the default anchor for
	// a new session and the first city of its announce set. AnnounceCities is
	// their saved default set. Both are per-user, delivered like Sizes.
	Timezone       string          `json:"timezone,omitempty"`
	AnnounceCities json.RawMessage `json:"announce_cities,omitempty"`
	// SessionTitleMode is how the reader wants a session's derived name written
	// (issue #8): "date-title" (the default), "title" or "date".
	SessionTitleMode string `json:"session_title_mode,omitempty"`
}

// unreadDTO flags, per card, whether the caller has unread events in either
// bucket (see migrateV7 / card_reads). Only cards with at least one true flag
// are included in the snapshot's Unread map.
type unreadDTO struct {
	Content  bool `json:"content"`
	Comments bool `json:"comments"`
}

func (s *server) handleGetBoard(w http.ResponseWriter, r *http.Request) {
	uid, bid, role, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	ctx := r.Context()
	snap := boardSnapshot{ID: bid, Role: role, Lists: []listDTO{}, Groups: []groupDTO{}, Cards: []cardDTO{}, Labels: []labelDTO{}, Sessions: []sessionDTO{}, CardLabels: []cardLabelDTO{}, CardSessions: []cardSessionDTO{}, TourTesters: []tourTesterDTO{}, Unread: map[string]unreadDTO{}}

	var name sql.NullString
	var nameEnc []byte
	if err := s.db.QueryRowContext(ctx, `select name, name_enc, schema_version from boards where id = ?`, bid).Scan(&name, &nameEnc, &snap.SchemaVersion); handleErr(w, err) {
		return
	}
	snap.Name = name.String
	snap.NameEnc = b64(nameEnc)

	// The caller's per-user display prefs (see boardSnapshot.Sizes /
	// .DefaultAuthor), shared across all their boards — keyed on the user, not
	// this board.
	prefs, err := loadUserPrefs(ctx, s.db, uid)
	if handleErr(w, err) {
		return
	}
	if prefs.Sizes.Valid && prefs.Sizes.String != "" {
		snap.Sizes = json.RawMessage(prefs.Sizes.String)
	}
	if prefs.AnnounceCities.Valid && prefs.AnnounceCities.String != "" {
		snap.AnnounceCities = json.RawMessage(prefs.AnnounceCities.String)
	}
	snap.DefaultAuthor = prefs.DefaultAuthor.String
	snap.CardTitle = prefs.CardTitle.String
	snap.Timezone = prefs.Timezone.String
	snap.SessionTitleMode = prefs.SessionTitleMode.String

	lists, err := scanLists(ctx, s.db, bid)
	if handleErr(w, err) {
		return
	}
	snap.Lists = lists

	groups, err := scanListGroups(ctx, s.db, bid)
	if handleErr(w, err) {
		return
	}
	snap.Groups = groups

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

	sessions, err := scanSessions(ctx, s.db, bid)
	if handleErr(w, err) {
		return
	}
	snap.Sessions = sessions

	clRows, err := s.db.QueryContext(ctx, `
select cl.card_id, cl.label_id, cl.session_id
from card_labels cl join cards c on c.id = cl.card_id
where c.board_id = ? and c.deleted_at is null`, bid)
	if handleErr(w, err) {
		return
	}
	defer clRows.Close()
	for clRows.Next() {
		var a cardLabelDTO
		var sessionID sql.NullInt64
		if err := clRows.Scan(&a.CardID, &a.LabelID, &sessionID); handleErr(w, err) {
			return
		}
		if sessionID.Valid {
			a.SessionID = &sessionID.Int64
		}
		snap.CardLabels = append(snap.CardLabels, a)
	}

	ttRows, err := s.db.QueryContext(ctx,
		`select list_id, group_id, session_id from tour_testers where board_id = ?`, bid)
	if handleErr(w, err) {
		return
	}
	defer ttRows.Close()
	for ttRows.Next() {
		var d tourTesterDTO
		var listID, groupID, sessionID sql.NullInt64
		if err := ttRows.Scan(&listID, &groupID, &sessionID); handleErr(w, err) {
			return
		}
		if sessionID.Valid {
			d.SessionID = &sessionID.Int64
		}
		if listID.Valid {
			d.ListID = &listID.Int64
		}
		if groupID.Valid {
			d.GroupID = &groupID.Int64
		}
		snap.TourTesters = append(snap.TourTesters, d)
	}

	csRows, err := s.db.QueryContext(ctx, `
select cs.card_id, cs.session_id
from card_sessions cs join cards c on c.id = cs.card_id
where c.board_id = ? and c.deleted_at is null`, bid)
	if handleErr(w, err) {
		return
	}
	defer csRows.Close()
	for csRows.Next() {
		var p cardSessionDTO
		if err := csRows.Scan(&p.CardID, &p.SessionID); handleErr(w, err) {
			return
		}
		snap.CardSessions = append(snap.CardSessions, p)
	}

	// Unread map: one row per card that has at least one event, authored by
	// someone else, past the caller's read watermark for that bucket. All-false
	// cards are omitted entirely to keep the payload small.
	unreadRows, err := s.db.QueryContext(ctx, `
select e.card_id,
  max(case when e.type =  'comment' and e.id > coalesce(cr.comment_read_id,0) then 1 else 0 end),
  max(case when e.type <> 'comment' and e.id > coalesce(cr.content_read_id,0) then 1 else 0 end)
from timeline_events e
join cards c on c.id = e.card_id and c.deleted_at is null
left join card_reads cr on cr.card_id = e.card_id and cr.user_id = ?
where e.board_id = ? and e.deleted_at is null and e.author_user_id is not null and e.author_user_id <> ?
group by e.card_id`, uid, bid, uid)
	if handleErr(w, err) {
		return
	}
	defer unreadRows.Close()
	for unreadRows.Next() {
		var cardID int64
		var commentsUnread, contentUnread int
		if err := unreadRows.Scan(&cardID, &commentsUnread, &contentUnread); handleErr(w, err) {
			return
		}
		if commentsUnread == 1 || contentUnread == 1 {
			snap.Unread[strconv.FormatInt(cardID, 10)] = unreadDTO{Content: contentUnread == 1, Comments: commentsUnread == 1}
		}
	}
	if err := unreadRows.Err(); handleErr(w, err) {
		return
	}
	writeJSON(w, snap)
}

type patchBoardRequest struct {
	Name *string `json:"name"` // plaintext new name
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
	if req.Name == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	name := strings.TrimSpace(*req.Name)
	if name == "" {
		httpError(w, http.StatusBadRequest, "empty name")
		return
	}
	// A rename writes the plaintext name and settles the board at schema_version 2
	// (renaming a legacy board is itself a migration off name_enc).
	err := s.withWriteTx(r.Context(), "patch-board", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update boards set name = ?, schema_version = 2, updated_at = ? where id = ?`,
			name, rfc3339(time.Now()), bid)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMigrateName backfills a legacy board's plaintext name from a client that
// holds the data key (it decrypts name_enc and posts the plaintext here). One-shot
// and retirable: it only ever touches schema_version = 1 rows, so a stale client
// replaying an old name can never clobber a real rename (which has already moved
// the board to version 2). A no-op 204 when the board is already migrated.
type migrateNameRequest struct {
	Name string `json:"name"`
}

func (s *server) handleMigrateName(w http.ResponseWriter, r *http.Request) {
	_, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	var req migrateNameRequest
	if !readJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		httpError(w, http.StatusBadRequest, "empty name")
		return
	}
	err := s.withWriteTx(r.Context(), "migrate-name", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`update boards set name = ?, schema_version = 2, updated_at = ? where id = ? and schema_version = 1`,
			name, rfc3339(time.Now()), bid)
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
		return tombstone(ctx, tx, "boards", "id = ?", bid)
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
