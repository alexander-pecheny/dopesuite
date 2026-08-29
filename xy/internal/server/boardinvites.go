package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"pecheny.me/dopecore/authcred"
)

// Board invite links (ADR-0017). A link grants MEMBERSHIP, never the key: the
// joiner lands on the passphrase overlay knowing only the board's name, which
// is the one field xy keeps in plaintext anyway. Owner-only to mint, revoke and
// approve, exactly like adding a member by username.
//
// A link may cap uses, expire, or require the owner's approval. Only a use that
// reached 'joined' spends the cap, so a queue of hopefuls behind a one-seat link
// costs nothing and a decline refunds nothing. A declined row stays, and its
// unique(invite_id, user_id) is what stops the declined asking again.

type invitePersonDTO struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	At       string `json:"at"`
}

type boardInviteDTO struct {
	ID               int64             `json:"id"`
	Code             string            `json:"code"`
	Label            string            `json:"label"`
	CreatedAt        string            `json:"created_at"`
	ExpiresAt        string            `json:"expires_at,omitempty"`
	MaxUses          *int64            `json:"max_uses"`
	Used             int64             `json:"used"`
	Left             *int64            `json:"left"`
	RequiresApproval bool              `json:"requires_approval"`
	State            string            `json:"state"`
	Joined           []invitePersonDTO `json:"joined"`
	Pending          []invitePersonDTO `json:"pending"`
}

// boardInvitePeekDTO is all an invitee learns before joining: which board this
// is, and whether the link still works for them.
type boardInvitePeekDTO struct {
	BoardID          int64  `json:"board_id"`
	BoardName        string `json:"board_name"`
	State            string `json:"state"`
	RequiresApproval bool   `json:"requires_approval"`
}

// inviteRow is one link as the DB holds it, with the counts a state needs.
type inviteRow struct {
	id               int64
	boardID          int64
	code             string
	label            sql.NullString
	createdAt        string
	expiresAt        sql.NullString
	maxUses          sql.NullInt64
	requiresApproval bool
	revokedAt        sql.NullString
	used             int64
}

const inviteSelect = `
select i.id, i.board_id, i.code, i.label, i.created_at, i.expires_at, i.max_uses,
       i.requires_approval, i.revoked_at,
       (select count(*) from board_invite_uses u where u.invite_id = i.id and u.status = 'joined')
from board_invites i
join boards b on b.id = i.board_id and b.deleted_at is null`

func scanInvite(sc interface{ Scan(...any) error }) (inviteRow, error) {
	var iv inviteRow
	err := sc.Scan(&iv.id, &iv.boardID, &iv.code, &iv.label, &iv.createdAt, &iv.expiresAt,
		&iv.maxUses, &iv.requiresApproval, &iv.revokedAt, &iv.used)
	return iv, err
}

// state is why a link does or does not work, in the order the reasons override
// each other: a revoked link is revoked whether or not it also expired.
func (iv inviteRow) state(now time.Time) string {
	switch {
	case iv.revokedAt.Valid:
		return "revoked"
	case iv.expiresAt.Valid && !now.Before(mustTime(iv.expiresAt.String)):
		return "expired"
	case iv.maxUses.Valid && iv.used >= iv.maxUses.Int64:
		return "exhausted"
	}
	return "active"
}

func (iv inviteRow) left() *int64 {
	if !iv.maxUses.Valid {
		return nil
	}
	n := iv.maxUses.Int64 - iv.used
	if n < 0 {
		n = 0
	}
	return &n
}

// mustTime parses a stored RFC3339 stamp; an unparseable one reads as long past,
// which fails a link closed rather than open.
func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (iv inviteRow) dto(now time.Time) boardInviteDTO {
	d := boardInviteDTO{
		ID: iv.id, Code: iv.code, Label: iv.label.String, CreatedAt: iv.createdAt,
		ExpiresAt: iv.expiresAt.String, Used: iv.used, Left: iv.left(),
		RequiresApproval: iv.requiresApproval, State: iv.state(now),
		Joined: []invitePersonDTO{}, Pending: []invitePersonDTO{},
	}
	if iv.maxUses.Valid {
		n := iv.maxUses.Int64
		d.MaxUses = &n
	}
	return d
}

// ---- owner side ----

// requireBoardOwner is requireBoard plus the rule that minting, revoking and
// approving belong to the owner alone, exactly like adding a member by username.
func (s *server) requireBoardOwner(w http.ResponseWriter, r *http.Request) (userID, boardID int64, ok bool) {
	uid, bid, role, ok := s.requireBoard(w, r, "id")
	if !ok {
		return 0, 0, false
	}
	if role != "owner" {
		httpError(w, http.StatusForbidden, ownerOnly)
		return 0, 0, false
	}
	return uid, bid, true
}

const ownerOnly = "только владелец управляет ссылками"

func (s *server) handleListBoardInvites(w http.ResponseWriter, r *http.Request) {
	_, bid, ok := s.requireBoardOwner(w, r)
	if !ok {
		return
	}
	out, err := s.boardInvites(r.Context(), bid)
	if handleErr(w, err) {
		return
	}
	writeJSON(w, out)
}

// boardInvites lists a board's links newest first, each with who joined through
// it and who is still waiting.
func (s *server) boardInvites(ctx context.Context, bid int64) ([]boardInviteDTO, error) {
	rows, err := s.db.QueryContext(ctx, inviteSelect+`
where i.board_id = ? order by i.id desc`, bid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now()
	out := []boardInviteDTO{}
	byID := map[int64]int{}
	for rows.Next() {
		iv, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		byID[iv.id] = len(out)
		out = append(out, iv.dto(now))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	useRows, err := s.db.QueryContext(ctx, `
select u.invite_id, u.user_id, coalesce(nullif(us.username, ''), us.telegram_username, ''), u.status,
       coalesce(u.decided_at, u.requested_at)
from board_invite_uses u
join board_invites i on i.id = u.invite_id
join users us on us.id = u.user_id
where i.board_id = ? and u.status in ('joined','pending')
order by u.id`, bid)
	if err != nil {
		return nil, err
	}
	defer useRows.Close()
	for useRows.Next() {
		var inviteID int64
		var m invitePersonDTO
		var status string
		if err := useRows.Scan(&inviteID, &m.UserID, &m.Username, &status, &m.At); err != nil {
			return nil, err
		}
		i, ok := byID[inviteID]
		if !ok {
			continue
		}
		if status == "joined" {
			out[i].Joined = append(out[i].Joined, m)
		} else {
			out[i].Pending = append(out[i].Pending, m)
		}
	}
	return out, useRows.Err()
}

// A year is longer than any link should live, and short enough that the hours
// cannot overflow the Duration they become. The label cap mirrors the field's
// own maxlength, which is a hint and not a check.
const (
	maxTTLHours    = 24 * 366
	maxInviteLabel = 100
)

type createInviteRequest struct {
	Label            string `json:"label"`
	MaxUses          int64  `json:"max_uses"`  // 0 = без ограничения
	TTLHours         int64  `json:"ttl_hours"` // 0 = без срока
	RequiresApproval bool   `json:"requires_approval"`
}

func (s *server) handleCreateBoardInvite(w http.ResponseWriter, r *http.Request) {
	uid, bid, ok := s.requireBoardOwner(w, r)
	if !ok {
		return
	}
	var req createInviteRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.MaxUses < 0 || req.TTLHours < 0 || req.TTLHours > maxTTLHours {
		httpError(w, http.StatusBadRequest, "ограничения вне допустимого диапазона")
		return
	}
	if len([]rune(req.Label)) > maxInviteLabel {
		httpError(w, http.StatusBadRequest, "название слишком длинное")
		return
	}
	code, err := authcred.NewInviteCode()
	if handleErr(w, err) {
		return
	}
	now := time.Now()
	var expires, maxUses any
	if req.TTLHours > 0 {
		expires = rfc3339(now.Add(time.Duration(req.TTLHours) * time.Hour))
	}
	if req.MaxUses > 0 {
		maxUses = req.MaxUses
	}
	var id int64
	err = s.withWriteTx(r.Context(), "create-board-invite", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
insert into board_invites(board_id, code, label, created_by, created_at, expires_at, max_uses, requires_approval)
values(?, ?, ?, ?, ?, ?, ?, ?)`,
			bid, code, nullStr(strings.TrimSpace(req.Label)), uid, rfc3339(now), expires, maxUses, req.RequiresApproval)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if handleErr(w, err) {
		return
	}
	iv, err := s.inviteByID(r.Context(), id)
	if handleErr(w, err) {
		return
	}
	writeJSON(w, iv.dto(now))
}

func (s *server) inviteByID(ctx context.Context, id int64) (inviteRow, error) {
	return scanInvite(s.db.QueryRowContext(ctx, inviteSelect+` where i.id = ?`, id))
}

// requireOwnedInvite resolves the {id} link and checks the caller owns its board.
func (s *server) requireOwnedInvite(w http.ResponseWriter, r *http.Request) (inviteRow, bool) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return inviteRow{}, false
	}
	id, ok := pathInt(w, r, "id")
	if !ok {
		return inviteRow{}, false
	}
	iv, err := s.inviteByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		httpError(w, http.StatusNotFound, "ссылка не найдена")
		return inviteRow{}, false
	}
	if handleErr(w, err) {
		return inviteRow{}, false
	}
	role, err := boardRole(r.Context(), s.db, iv.boardID, u.UserID)
	if handleErr(w, err) {
		return inviteRow{}, false
	}
	if role != "owner" {
		httpError(w, http.StatusForbidden, ownerOnly)
		return inviteRow{}, false
	}
	return iv, true
}

func (s *server) handleRevokeBoardInvite(w http.ResponseWriter, r *http.Request) {
	iv, ok := s.requireOwnedInvite(w, r)
	if !ok {
		return
	}
	err := s.withWriteTx(r.Context(), "revoke-board-invite", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
update board_invites set revoked_at = ? where id = ? and revoked_at is null`, rfc3339(time.Now()), iv.id)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteBoardInvite drops the row and its use history. The members it
// admitted stay members: a link is how they arrived, not what keeps them in.
func (s *server) handleDeleteBoardInvite(w http.ResponseWriter, r *http.Request) {
	iv, ok := s.requireOwnedInvite(w, r)
	if !ok {
		return
	}
	err := s.withWriteTx(r.Context(), "delete-board-invite", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `delete from board_invite_uses where invite_id = ?`, iv.id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `delete from board_invites where id = ?`, iv.id)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type decideJoinRequest struct {
	Decision string `json:"decision"` // approve | decline
}

func (s *server) handleDecideJoinRequest(w http.ResponseWriter, r *http.Request) {
	_, bid, ok := s.requireBoardOwner(w, r)
	if !ok {
		return
	}
	requesterID, ok := pathInt(w, r, "userId")
	if !ok {
		return
	}
	var req decideJoinRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.Decision != "approve" && req.Decision != "decline" {
		httpError(w, http.StatusBadRequest, "решение должно быть approve или decline")
		return
	}
	now := time.Now()
	err := s.withWriteTx(r.Context(), "decide-join-request", func(ctx context.Context, tx *sql.Tx) error {
		iv, use, err := pendingRequest(ctx, tx, bid, requesterID)
		if err != nil {
			return err
		}
		if req.Decision == "decline" {
			_, err := tx.ExecContext(ctx, `
update board_invite_uses set status = 'declined', decided_at = ? where id = ?`, rfc3339(now), use)
			return err
		}
		// Approving still has to fit under the cap: the queue was allowed to grow
		// past it, so the seats may have gone to earlier approvals in the meantime.
		if iv.maxUses.Valid && iv.used >= iv.maxUses.Int64 {
			return errBadRequest("в ссылке не осталось мест")
		}
		if _, err := tx.ExecContext(ctx, `
update board_invite_uses set status = 'joined', decided_at = ? where id = ?`, rfc3339(now), use); err != nil {
			return err
		}
		return addMember(ctx, tx, bid, requesterID)
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pendingRequest finds the one waiting request this user has on this board, and
// the link it came in through.
func pendingRequest(ctx context.Context, tx *sql.Tx, bid, userID int64) (inviteRow, int64, error) {
	iv, err := scanInvite(tx.QueryRowContext(ctx, inviteSelect+`
join board_invite_uses u on u.invite_id = i.id
where i.board_id = ? and u.user_id = ? and u.status = 'pending'`, bid, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return iv, 0, errBadRequest("заявка не найдена")
	}
	if err != nil {
		return iv, 0, err
	}
	var useID int64
	err = tx.QueryRowContext(ctx, `
select id from board_invite_uses where invite_id = ? and user_id = ?`, iv.id, userID).Scan(&useID)
	return iv, useID, err
}

func addMember(ctx context.Context, tx *sql.Tx, bid, userID int64) error {
	_, err := tx.ExecContext(ctx, `
insert into board_members(board_id, user_id, role) values(?, ?, 'editor')
on conflict(board_id, user_id) do nothing`, bid, userID)
	return err
}

// ---- invitee side ----

// inviteState is what the link does for THIS caller: the link's own state,
// unless their history overrides it.
//
// Waiting is board-wide, not per-link: one person is one Join Request, and the
// owner decides about a person. Scoped to the link, someone could queue twice
// through two links and be approved into two seats — and a decide would pick
// between their rows arbitrarily.
//
// A refusal and a spent passage are per-link, because both are about this link:
// a decline is final for it (another link lets the owner change their mind), and
// a link that already admitted someone the owner has since removed must not
// quietly let them back in.
func inviteState(ctx context.Context, q rowQuerier, iv inviteRow, userID int64, now time.Time) (string, error) {
	var role string
	err := q.QueryRowContext(ctx, `
select role from board_members where board_id = ? and user_id = ?`, iv.boardID, userID).Scan(&role)
	if err == nil {
		return "member", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var waiting int
	if err := q.QueryRowContext(ctx, `
select count(*) from board_invite_uses u join board_invites i on i.id = u.invite_id
where i.board_id = ? and u.user_id = ? and u.status = 'pending'`, iv.boardID, userID).Scan(&waiting); err != nil {
		return "", err
	}
	if waiting > 0 {
		return "pending", nil
	}
	var status string
	err = q.QueryRowContext(ctx, `
select status from board_invite_uses where invite_id = ? and user_id = ?`, iv.id, userID).Scan(&status)
	if err == nil {
		if status == "declined" {
			return "declined", nil
		}
		return "spent", nil // 'joined', and they are not a member: the owner removed them
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return iv.state(now), nil
}

func (s *server) inviteByCode(ctx context.Context, code string) (inviteRow, error) {
	return scanInvite(s.db.QueryRowContext(ctx, inviteSelect+` where i.code = ?`, code))
}

func (s *server) handlePeekInvite(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	iv, err := s.inviteByCode(r.Context(), r.PathValue("code"))
	if errors.Is(err, sql.ErrNoRows) {
		httpError(w, http.StatusNotFound, "ссылка не найдена")
		return
	}
	if handleErr(w, err) {
		return
	}
	state, err := inviteState(r.Context(), s.db, iv, u.UserID, time.Now())
	if handleErr(w, err) {
		return
	}
	name, err := boardDisplayName(r.Context(), s.db, iv.boardID)
	if handleErr(w, err) {
		return
	}
	writeJSON(w, boardInvitePeekDTO{
		BoardID: iv.boardID, BoardName: name, State: state, RequiresApproval: iv.requiresApproval,
	})
}

// boardDisplayName is the plaintext board name (schema_version 2). A legacy
// board still holds its name in name_enc, and the invitee has no key — so it
// gets no name at all rather than a lie.
func boardDisplayName(ctx context.Context, q rowQuerier, bid int64) (string, error) {
	var name sql.NullString
	var version int
	if err := q.QueryRowContext(ctx, `select name, schema_version from boards where id = ?`, bid).Scan(&name, &version); err != nil {
		return "", err
	}
	if version < 2 {
		return "", nil
	}
	return name.String, nil
}

type joinInviteResponse struct {
	BoardID int64  `json:"board_id"`
	State   string `json:"state"` // member | pending
}

func (s *server) handleJoinInvite(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	code := r.PathValue("code")
	now := time.Now()
	var out joinInviteResponse
	var notify *joinRequestNudge
	err := s.withWriteTx(r.Context(), "join-invite", func(ctx context.Context, tx *sql.Tx) error {
		iv, err := scanInvite(tx.QueryRowContext(ctx, inviteSelect+` where i.code = ?`, code))
		if errors.Is(err, sql.ErrNoRows) {
			return &appError{status: http.StatusNotFound, msg: "ссылка не найдена"}
		}
		if err != nil {
			return err
		}
		out.BoardID = iv.boardID
		// The state is re-read inside the write tx, so two people racing the last
		// seat cannot both take it.
		state, err := inviteState(ctx, tx, iv, u.UserID, now)
		if err != nil {
			return err
		}
		switch state {
		case "member":
			out.State = "member"
			return nil
		case "pending":
			out.State = "pending"
			return nil
		case "active":
		default:
			return errBadRequest(inviteRefusal(state))
		}
		status := "joined"
		if iv.requiresApproval {
			status = "pending"
		}
		if _, err := tx.ExecContext(ctx, `
insert into board_invite_uses(invite_id, user_id, status, requested_at, decided_at)
values(?, ?, ?, ?, ?)`, iv.id, u.UserID, status, rfc3339(now), nullIf(status == "pending", rfc3339(now))); err != nil {
			return err
		}
		out.State = "member"
		if status == "pending" {
			out.State = "pending"
			notify = &joinRequestNudge{boardID: iv.boardID, requester: u.UserID}
			return nil
		}
		return addMember(ctx, tx, iv.boardID, u.UserID)
	})
	if handleErr(w, err) {
		return
	}
	if notify != nil {
		s.notifyJoinRequest(notify.boardID, notify.requester)
	}
	writeJSON(w, out)
}

type joinRequestNudge struct {
	boardID   int64
	requester int64
}

// nullIf writes NULL when the condition holds — a pending row has no decision yet.
func nullIf(cond bool, v string) any {
	if cond {
		return nil
	}
	return v
}

// inviteRefusal words a dead link for the person holding it.
func inviteRefusal(state string) string {
	switch state {
	case "revoked":
		return "ссылка отозвана"
	case "expired":
		return "срок ссылки истёк"
	case "exhausted":
		return "ссылка исчерпана"
	case "declined":
		return "владелец отклонил заявку"
	case "spent":
		return "по этой ссылке вы уже проходили — попросите новую"
	}
	return "ссылка не работает"
}
