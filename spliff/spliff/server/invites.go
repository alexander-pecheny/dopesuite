package spliffserver

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"pecheny.me/dopecore/authcred"
	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopecore/invitelink"

	"spliff/spliff/storage/store"
	"spliff/spliff/web/route"

	spliffstrings "spliff/i18nstrings"
)

// A Group admits people exactly as an xy board does, so the machine is
// dopecore/invitelink (root docs/adr/0004): the use cap, the expiry, the
// approval queue, the state ladder and the SQL on the two tables all live
// there. This file is the adapter — Spliff's routes, Spliff's DTOs, Spliff's
// write transaction, Spliff's words, and what a Group is.

// groupScope teaches invitelink what a Spliff Group is. claim carries the one
// thing the package has no opinion about: the joiner may be stepping into a
// Phantom's shoes rather than taking a new seat.
type groupScope struct {
	s     *server
	claim int64
}

func (g groupScope) IsMember(ctx context.Context, q invitelink.Querier, groupID, userID int64) (bool, error) {
	return store.IsMember(ctx, q, groupID, userID)
}

// AddMember seats the joiner. Claiming a Phantom is not a second seat: the
// member row already exists, holding its Payments, its Shares and its place in
// join order, and all it gains is an account. So nothing is inserted and no
// balance moves.
func (g groupScope) AddMember(ctx context.Context, tx invitelink.Tx, groupID, userID int64) error {
	if g.claim != 0 {
		return store.ClaimPhantom(ctx, tx, groupID, g.claim, userID)
	}
	return store.AddMember(ctx, tx, groupID, userID, rfc3339(time.Now()))
}

// DisplayName is all an invitee learns before joining: the Group's name. Unlike
// an xy board's, it is always plaintext — there is nothing encrypted in Spliff.
func (g groupScope) DisplayName(ctx context.Context, q invitelink.Querier, groupID int64) (string, error) {
	group, err := store.GroupByID(ctx, q, groupID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return group.Name, err
}

func (g groupScope) Names(ctx context.Context, q invitelink.Querier, ids []int64) (map[int64]string, error) {
	return store.UserNames(ctx, q, ids)
}

func (g groupScope) NudgeOwner(groupID, requesterID int64) {
	g.s.notifyJoinRequest(groupID, requesterID)
}

// invites is the shared machine bound to Spliff's tables. The join is what
// keeps a deleted Group's links from resolving: they live in group_invites
// alone and, but for it, a link would go on answering after its Group had gone.
func (s *server) invites() invitelink.Links { return s.invitesClaiming(0) }

// invitesClaiming is the same machine with the joiner's "I am …" answer bound
// to the seat it takes.
func (s *server) invitesClaiming(claim int64) invitelink.Links {
	return invitelink.Links{
		Invites:   "group_invites",
		Uses:      "group_invite_uses",
		ScopeID:   "group_id",
		ScopeJoin: "join groups g on g.id = i.group_id",
		Scope:     groupScope{s: s, claim: claim},
	}
}

type invitePersonDTO struct {
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	At     string `json:"at"`
}

type inviteDTO struct {
	ID               int64             `json:"id"`
	Code             string            `json:"code"`
	URL              string            `json:"url"`
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

// invitePeekDTO is all an invitee learns before joining: which Group this is,
// whether the link still works for them, and the Phantoms they might be. The
// last is deliberately public — a name at the table is what the joiner has to
// recognise before they have an account, and it is the Owner who typed it.
type invitePeekDTO struct {
	GroupID          int64        `json:"group_id"`
	GroupName        string       `json:"group_name"`
	State            string       `json:"state"`
	RequiresApproval bool         `json:"requires_approval"`
	Phantoms         []phantomDTO `json:"phantoms"`
}

// phantomDTO is one "I am …" the join page can offer.
type phantomDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (s *server) phantomsOf(ctx context.Context, groupID int64) ([]phantomDTO, error) {
	list, err := store.Phantoms(ctx, s.db, groupID)
	if err != nil {
		return nil, err
	}
	out := []phantomDTO{}
	for _, m := range list {
		out = append(out, phantomDTO{ID: m.ID, Name: m.Name})
	}
	return out, nil
}

func toInviteDTO(lk invitelink.Link, now time.Time) inviteDTO {
	return inviteDTO{
		ID: lk.ID, Code: lk.Code, URL: publicURL() + "/join/" + lk.Code,
		Label: lk.Label, CreatedAt: lk.CreatedAt, ExpiresAt: lk.ExpiresAt,
		MaxUses: lk.MaxUses, Used: lk.Used, Left: lk.Left(),
		RequiresApproval: lk.RequiresApproval, State: string(lk.State(now)),
		Joined: people(lk.Joined), Pending: people(lk.Waiting),
	}
}

func people(in []invitelink.Person) []invitePersonDTO {
	out := []invitePersonDTO{}
	for _, p := range in {
		out = append(out, invitePersonDTO{UserID: p.UserID, Name: p.Name, At: p.At})
	}
	return out
}

// ---- the Owner's side ----

func (s *server) handleListInvites(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	links, err := s.invites().List(r.Context(), s.db, sc.GroupID)
	if err != nil {
		return err
	}
	now := time.Now()
	out := []inviteDTO{}
	for _, lk := range links {
		out = append(out, toInviteDTO(lk, now))
	}
	return writeJSON(w, out)
}

type createInviteRequest struct {
	Label            string `json:"label"`
	MaxUses          int64  `json:"max_uses"`  // 0 = unlimited
	TTLHours         int64  `json:"ttl_hours"` // 0 = no expiry
	RequiresApproval bool   `json:"requires_approval"`
}

func (s *server) handleCreateInvite(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req createInviteRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	opt := invitelink.Options{
		Label: req.Label, MaxUses: req.MaxUses, TTLHours: req.TTLHours,
		RequiresApproval: req.RequiresApproval,
	}
	if err := opt.Validate(); err != nil {
		return corei18n.User(inviteBadRequest(err))
	}
	code, err := authcred.NewInviteCode()
	if err != nil {
		return err
	}
	now := time.Now()
	links := s.invites()
	var id int64
	err = s.withWriteTx(r.Context(), "create-invite", func(ctx context.Context, tx *sql.Tx) error {
		var err error
		id, err = links.Mint(ctx, tx, sc.GroupID, sc.User.UserID, code, opt, now)
		return err
	})
	if err != nil {
		return err
	}
	lk, err := links.ByID(r.Context(), s.db, id)
	if err != nil {
		return err
	}
	return writeJSON(w, toInviteDTO(lk, now))
}

// inviteBadRequest words the two settings a link's own limits must respect.
func inviteBadRequest(err error) string {
	str := spliffstrings.Default
	if errors.Is(err, invitelink.ErrLabelTooLong) {
		return str.Invite.Error.LabelTooLong()
	}
	return str.Invite.Error.LimitsOutOfRange()
}

// requireOwnedInvite resolves the {id} link and checks the caller owns its
// Group. It cannot go through the dispatcher's {group}, because a link's URL
// names the link and nothing else.
func (s *server) requireOwnedInvite(r *http.Request, sc route.Scope) (invitelink.Link, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return invitelink.Link{}, route.NotFound(spliffstrings.Default.Invite.Error.NotFound())
	}
	lk, err := s.invites().ByID(r.Context(), s.db, id)
	if errors.Is(err, invitelink.ErrNotFound) {
		return lk, route.NotFound(spliffstrings.Default.Invite.Error.NotFound())
	}
	if err != nil {
		return lk, err
	}
	g, err := store.GroupByID(r.Context(), s.db, lk.ScopeID)
	if err != nil {
		return lk, notFound(err)
	}
	if g.OwnerID != sc.User.UserID {
		return lk, route.Forbidden(spliffstrings.Default.Group.Error.OwnerOnly())
	}
	return lk, nil
}

func (s *server) handleRevokeInvite(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	lk, err := s.requireOwnedInvite(r, sc)
	if err != nil {
		return err
	}
	links := s.invites()
	if err := s.withWriteTx(r.Context(), "revoke-invite", func(ctx context.Context, tx *sql.Tx) error {
		return links.Revoke(ctx, tx, lk.ID, time.Now())
	}); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleDeleteInvite drops the row and its use history. The Members it admitted
// stay Members: a link is how they arrived, not what keeps them in.
func (s *server) handleDeleteInvite(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	lk, err := s.requireOwnedInvite(r, sc)
	if err != nil {
		return err
	}
	links := s.invites()
	if err := s.withWriteTx(r.Context(), "delete-invite", func(ctx context.Context, tx *sql.Tx) error {
		return links.Delete(ctx, tx, lk.ID)
	}); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type decideRequest struct {
	Decision string `json:"decision"` // approve | decline
}

func (s *server) handleDecideJoinRequest(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	str := spliffstrings.Default
	requesterID, err := strconv.ParseInt(r.PathValue("userId"), 10, 64)
	if err != nil {
		return route.NotFound(str.Invite.Error.RequestNotFound())
	}
	var req decideRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Decision != "approve" && req.Decision != "decline" {
		return corei18n.User(str.Invite.Error.DecisionInvalid())
	}
	links := s.invites()
	now := time.Now()
	err = s.withWriteTx(r.Context(), "decide-join-request", func(ctx context.Context, tx *sql.Tx) error {
		return links.Decide(ctx, tx, sc.GroupID, requesterID, req.Decision == "approve", now)
	})
	if err != nil {
		return decideError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// decideError gives the package's two refusals their Spliff wording; anything
// else passes through as itself.
func decideError(err error) error {
	str := spliffstrings.Default
	switch {
	case errors.Is(err, invitelink.ErrRequestNotFound):
		return corei18n.User(str.Invite.Error.RequestNotFound())
	case errors.Is(err, invitelink.ErrNoSeatsLeft):
		return corei18n.User(str.Invite.Error.NoSeatsLeft())
	}
	return err
}

// ---- the invitee's side ----

func (s *server) handlePeekInvite(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	peek, err := s.invites().PeekCode(r.Context(), s.db, r.PathValue("code"), sc.User.UserID, time.Now())
	if errors.Is(err, invitelink.ErrNotFound) {
		return route.NotFound(spliffstrings.Default.Invite.Error.NotFound())
	}
	if err != nil {
		return err
	}
	phantoms, err := s.phantomsOf(r.Context(), peek.ScopeID)
	if err != nil {
		return err
	}
	return writeJSON(w, invitePeekDTO{
		GroupID: peek.ScopeID, GroupName: peek.ScopeName,
		State: string(peek.State), RequiresApproval: peek.RequiresApproval,
		Phantoms: phantoms,
	})
}

// handlePublicPeek is what an anonymous visitor at an Invite Link sees: the
// Group's name, and nothing else. It is deliberately not the logged-in peek —
// that one answers what the link does for YOU, and there is no you yet.
func (s *server) handlePublicPeek(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	lk, err := s.invites().ByCode(r.Context(), s.db, r.PathValue("code"))
	if errors.Is(err, invitelink.ErrNotFound) {
		return route.NotFound(spliffstrings.Default.Invite.Error.NotFound())
	}
	if err != nil {
		return err
	}
	name, err := groupScope{s: s}.DisplayName(r.Context(), s.db, lk.ScopeID)
	if err != nil {
		return err
	}
	phantoms, err := s.phantomsOf(r.Context(), lk.ScopeID)
	if err != nil {
		return err
	}
	return writeJSON(w, invitePeekDTO{
		GroupID: lk.ScopeID, GroupName: name,
		State: string(lk.State(time.Now())), RequiresApproval: lk.RequiresApproval,
		Phantoms: phantoms,
	})
}

type joinResponse struct {
	GroupID int64  `json:"group_id"`
	State   string `json:"state"` // member | pending
}

// joinRequest is what the join page sends. Claim is the member row of the
// Phantom the joiner says they are; 0 is "I am myself".
type joinRequest struct {
	Claim int64 `json:"claim"`
}

func (s *server) handleJoinInvite(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	code := r.PathValue("code")
	var req joinRequest
	if r.ContentLength > 0 {
		if err := readJSON(r, &req); err != nil {
			return err
		}
	}
	now := time.Now()
	links := s.invitesClaiming(req.Claim)
	var res invitelink.Result
	err := s.withWriteTx(r.Context(), "join-invite", func(ctx context.Context, tx *sql.Tx) error {
		if req.Claim != 0 {
			if err := s.checkClaimable(ctx, tx, code, req.Claim, sc.User.UserID); err != nil {
				return err
			}
		}
		var err error
		res, err = links.Join(ctx, tx, code, sc.User.UserID, now)
		return err
	})
	if err != nil {
		return joinError(err)
	}
	if res.Nudge {
		links.Nudge(res.ScopeID, sc.User.UserID)
	}
	return writeJSON(w, joinResponse{GroupID: res.ScopeID, State: string(res.State)})
}

// checkClaimable is every way "I am <phantom>" can be wrong, checked inside the
// write transaction so that two people cannot claim the same Phantom. The
// refusals are the three the spec names, plus the one the approval queue forces:
// a Join Request is decided later, with nowhere to keep what the joiner claimed.
func (s *server) checkClaimable(ctx context.Context, tx *sql.Tx, code string, claim, userID int64) error {
	str := spliffstrings.Default
	lk, err := s.invites().ByCode(ctx, tx, code)
	if errors.Is(err, invitelink.ErrNotFound) {
		return route.NotFound(str.Invite.Error.NotFound())
	}
	if err != nil {
		return err
	}
	if lk.RequiresApproval {
		return corei18n.User(str.Invite.Error.ClaimNeedsDirectLink())
	}
	already, err := store.IsMember(ctx, tx, lk.ScopeID, userID)
	if err != nil {
		return err
	}
	if already {
		return corei18n.User(str.Invite.Error.AlreadyAMember())
	}
	member, err := store.MemberByID(ctx, tx, lk.ScopeID, claim)
	if errors.Is(err, store.ErrNotFound) {
		return corei18n.User(str.Invite.Error.NotAPhantom())
	}
	if err != nil {
		return err
	}
	if !member.IsPhantom() {
		return corei18n.User(str.Invite.Error.NotAPhantom())
	}
	return nil
}

// joinError maps the package's answers onto Spliff's edge: a missing link is a
// 404, a dead one a 400 worded for the person holding it.
func joinError(err error) error {
	if errors.Is(err, invitelink.ErrNotFound) {
		return route.NotFound(spliffstrings.Default.Invite.Error.NotFound())
	}
	var refused *invitelink.Refused
	if errors.As(err, &refused) {
		return corei18n.User(inviteRefusal(refused.State))
	}
	return err
}

// inviteRefusal words a dead link for the person holding it.
func inviteRefusal(state invitelink.State) string {
	str := spliffstrings.Default
	switch state {
	case invitelink.Revoked:
		return str.Invite.Refusal.Revoked()
	case invitelink.Expired:
		return str.Invite.Refusal.Expired()
	case invitelink.Exhausted:
		return str.Invite.Refusal.Exhausted()
	case invitelink.Declined:
		return str.Invite.Refusal.Declined()
	case invitelink.Spent:
		return str.Invite.Refusal.Spent()
	}
	return str.Invite.Refusal.Generic()
}
