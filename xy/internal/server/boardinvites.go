package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"pecheny.me/dopecore/authcred"
	"pecheny.me/dopecore/invitelink"

	xystrings "xy/i18nstrings"
)

// Board invite links (ADR-0017). A link grants MEMBERSHIP, never the key: the
// joiner lands on the passphrase overlay knowing only the board's name, which
// is the one field xy keeps in plaintext anyway. Owner-only to mint, revoke and
// approve, exactly like adding a member by username.
//
// The link's own machine — the use cap, the expiry, the approval queue, the
// states and the SQL on board_invites/board_invite_uses — is dopecore's
// (invitelink, root docs/adr/0004). This file is the adapter: xy's routes, xy's
// DTOs, xy's write transaction, xy's words and what a board is.

// boardScope teaches invitelink what an xy board is.
type boardScope struct{ s *server }

func (b boardScope) IsMember(ctx context.Context, q invitelink.Querier, bid, userID int64) (bool, error) {
	var role string
	err := q.QueryRowContext(ctx, `
select role from board_members where board_id = ? and user_id = ?`, bid, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (b boardScope) AddMember(ctx context.Context, tx invitelink.Tx, bid, userID int64) error {
	_, err := tx.ExecContext(ctx, `
insert into board_members(board_id, user_id, role) values(?, ?, 'editor')
on conflict(board_id, user_id) do nothing`, bid, userID)
	return err
}

func (b boardScope) DisplayName(ctx context.Context, q invitelink.Querier, bid int64) (string, error) {
	return boardDisplayName(ctx, q, bid)
}

// Names is how a person is written in the owner's list: their username, else
// the telegram one they arrived with.
func (b boardScope) Names(ctx context.Context, q invitelink.Querier, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := q.QueryContext(ctx, `
select id, coalesce(nullif(username, ''), telegram_username, '') from users
where id in (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

func (b boardScope) NudgeOwner(bid, requesterID int64) { b.s.notifyJoinRequest(bid, requesterID) }

// invites is the shared machine bound to xy's tables. The join is what keeps a
// deleted board's links from resolving: they live in board_invites alone and
// would never see boards.deleted_at.
func (s *server) invites() invitelink.Links {
	return invitelink.Links{
		Invites:   "board_invites",
		Uses:      "board_invite_uses",
		ScopeID:   "board_id",
		ScopeJoin: "join boards b on b.id = i.board_id and b.deleted_at is null",
		Scope:     boardScope{s},
	}
}

// boardInvitePeekDTO is all an invitee learns before joining: which board this
// is, and whether the link still works for them.
type boardInvitePeekDTO struct {
	BoardID          int64  `json:"board_id"`
	BoardName        string `json:"board_name"`
	State            string `json:"state"`
	RequiresApproval bool   `json:"requires_approval"`
}

// inviteTexts is xy's wording for the shared invite edge.
func inviteTexts() invitelink.Texts {
	str := xystrings.Default
	return invitelink.Texts{
		Revoked:          str.Server.Refusal.Revoked(),
		Expired:          str.Server.Refusal.Expired(),
		Exhausted:        str.Server.Refusal.Exhausted(),
		Declined:         str.Server.Refusal.Declined(),
		Spent:            str.Server.Refusal.Spent(),
		Broken:           str.Server.Refusal.Broken(),
		NotFound:         str.Server.Invite.NotFound(),
		RequestNotFound:  str.Server.Invite.RequestNotFound(),
		NoSeatsLeft:      str.Server.Invite.NoSeatsLeft(),
		LabelTooLong:     str.Server.Invite.LabelTooLong(),
		LimitsOutOfRange: str.Server.Invite.LimitsOutOfRange(),
		DecisionInvalid:  str.Server.Invite.DecisionInvalid(),
	}
}

// inviteError answers one of invitelink's errors with its status and xy's
// sentence; any other error passes through as itself.
func inviteError(err error) error {
	if a, ok := inviteTexts().Answer(err); ok {
		return &appError{status: a.Status, msg: a.Msg}
	}
	return err
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
		httpError(w, http.StatusForbidden, xystrings.Default.Server.Invite.OwnerOnly())
		return 0, 0, false
	}
	return uid, bid, true
}

func (s *server) handleListBoardInvites(w http.ResponseWriter, r *http.Request) {
	_, bid, ok := s.requireBoardOwner(w, r)
	if !ok {
		return
	}
	links, err := s.invites().List(r.Context(), s.db, bid)
	if handleErr(w, err) {
		return
	}
	writeJSON(w, invitelink.Views(links, time.Now()))
}

func (s *server) handleCreateBoardInvite(w http.ResponseWriter, r *http.Request) {
	uid, bid, ok := s.requireBoardOwner(w, r)
	if !ok {
		return
	}
	var req invitelink.MintRequest
	if !readJSON(w, r, &req) {
		return
	}
	opt, err := req.Options()
	if handleErr(w, inviteError(err)) {
		return
	}
	code, err := authcred.NewInviteCode()
	if handleErr(w, err) {
		return
	}
	now := time.Now()
	links := s.invites()
	var id int64
	err = s.withWriteTx(r.Context(), "create-board-invite", func(ctx context.Context, tx *sql.Tx) error {
		var err error
		id, err = links.Mint(ctx, tx, bid, uid, code, opt, now)
		return err
	})
	if handleErr(w, err) {
		return
	}
	lk, err := links.ByID(r.Context(), s.db, id)
	if handleErr(w, err) {
		return
	}
	writeJSON(w, lk.View(now))
}

// requireOwnedInvite resolves the {id} link and checks the caller owns its board.
func (s *server) requireOwnedInvite(w http.ResponseWriter, r *http.Request) (invitelink.Link, bool) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return invitelink.Link{}, false
	}
	id, ok := pathInt(w, r, "id")
	if !ok {
		return invitelink.Link{}, false
	}
	lk, err := s.invites().ByID(r.Context(), s.db, id)
	if handleErr(w, inviteError(err)) {
		return invitelink.Link{}, false
	}
	role, err := boardRole(r.Context(), s.db, lk.ScopeID, u.UserID)
	if handleErr(w, err) {
		return invitelink.Link{}, false
	}
	if role != "owner" {
		httpError(w, http.StatusForbidden, xystrings.Default.Server.Invite.OwnerOnly())
		return invitelink.Link{}, false
	}
	return lk, true
}

func (s *server) handleRevokeBoardInvite(w http.ResponseWriter, r *http.Request) {
	lk, ok := s.requireOwnedInvite(w, r)
	if !ok {
		return
	}
	links := s.invites()
	err := s.withWriteTx(r.Context(), "revoke-board-invite", func(ctx context.Context, tx *sql.Tx) error {
		return links.Revoke(ctx, tx, lk.ID, time.Now())
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteBoardInvite drops the row and its use history. The members it
// admitted stay members: a link is how they arrived, not what keeps them in.
func (s *server) handleDeleteBoardInvite(w http.ResponseWriter, r *http.Request) {
	lk, ok := s.requireOwnedInvite(w, r)
	if !ok {
		return
	}
	links := s.invites()
	err := s.withWriteTx(r.Context(), "delete-board-invite", func(ctx context.Context, tx *sql.Tx) error {
		return links.Delete(ctx, tx, lk.ID)
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	var req invitelink.DecideRequest
	if !readJSON(w, r, &req) {
		return
	}
	approve, err := req.Approve()
	if handleErr(w, inviteError(err)) {
		return
	}
	links := s.invites()
	now := time.Now()
	err = s.withWriteTx(r.Context(), "decide-join-request", func(ctx context.Context, tx *sql.Tx) error {
		return links.Decide(ctx, tx, bid, requesterID, approve, now)
	})
	if handleErr(w, inviteError(err)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- invitee side ----

func (s *server) handlePeekInvite(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	peek, err := s.invites().PeekCode(r.Context(), s.db, r.PathValue("code"), u.UserID, time.Now())
	if handleErr(w, inviteError(err)) {
		return
	}
	writeJSON(w, boardInvitePeekDTO{
		BoardID: peek.ScopeID, BoardName: peek.ScopeName,
		State: string(peek.State), RequiresApproval: peek.RequiresApproval,
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
	links := s.invites()
	var res invitelink.Result
	err := s.withWriteTx(r.Context(), "join-invite", func(ctx context.Context, tx *sql.Tx) error {
		var err error
		res, err = links.Join(ctx, tx, code, u.UserID, now)
		return err
	})
	if handleErr(w, inviteError(err)) {
		return
	}
	if res.Nudge {
		links.Nudge(res.ScopeID, u.UserID)
	}
	writeJSON(w, joinInviteResponse{BoardID: res.ScopeID, State: string(res.State)})
}
