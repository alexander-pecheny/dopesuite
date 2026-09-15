// Package route is the one dispatcher behind Spliff's routers. A Table holds
// routes as data — a Go 1.22 mux pattern, the access the route asks of its
// caller, the handler — and resolves what every handler would otherwise
// resolve by hand: the Group or Transaction named in the path, the session,
// and whether the caller is a Member or the Owner. A handler takes the
// resolved Scope and returns an error; the dispatcher writes it, so a status
// is decided in one place.
//
// It is dope's web/route, cut down to the four things Spliff asks of a caller.
package route

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"pecheny.me/dopecore/session"
)

// Access is what a route asks of its caller.
//
//   - Public: anybody, logged in or not.
//   - LoggedIn: a session, and nothing about any Group.
//   - Member: a Member of the Group in the path.
//   - Owner: the Owner of the Group in the path — minting and revoking Invite
//     Links, deciding Join Requests, kicking, handing over, deleting.
type Access int

const (
	Public Access = iota
	LoggedIn
	Member
	Owner
)

// Scope is what the dispatcher resolved before calling the handler. GroupID
// and TxID are 0 on routes whose pattern does not name them.
type Scope struct {
	User    session.User
	HasUser bool
	GroupID int64
	TxID    int64
	IsOwner bool
}

// Handler serves one route. An error it returns is written by Deps.WriteError.
type Handler func(w http.ResponseWriter, r *http.Request, sc Scope) error

// Denial is why the dispatcher refused a request before the handler ran.
type Denial int

const (
	NoSession Denial = iota + 1
	NotMember        // a session, but not in this Group
	NotOwner         // a Member, but not the Owner
	NoGroup          // no such Group, or no such Transaction
	CrossOrigin
)

// Deps is what the dispatcher needs from the app: how to find the caller, what
// a Group is, and how to write a refusal and an error.
type Deps struct {
	// Session resolves the request's cookie. It may write to w (the sliding
	// session's refreshed cookie), which is why it is handed one.
	Session func(w http.ResponseWriter, r *http.Request) (session.User, bool)
	// Membership answers whether this person is in the Group and whether they
	// own it. A Group that does not exist answers false, false, nil.
	Membership func(ctx context.Context, groupID, userID int64) (member, owner bool, err error)
	// GroupOfTransaction is the Group a Transaction belongs to; 0 when there is
	// no such Transaction.
	GroupOfTransaction func(ctx context.Context, txID int64) (int64, error)
	// WriteError turns a handler's error into a response.
	WriteError func(w http.ResponseWriter, r *http.Request, err error)
}

// Table is a set of routes sharing one refusal policy: the API answers a
// status, the pages send a logged-out visitor to /login carrying where they
// were going.
type Table struct {
	Deps Deps
	Mux  *http.ServeMux
	Deny func(w http.ResponseWriter, r *http.Request, d Denial)
}

// New builds a table over an existing mux, so the API and the page tables can
// share one. deny nil means DenyAPI.
func New(mux *http.ServeMux, deps Deps, deny func(w http.ResponseWriter, r *http.Request, d Denial)) *Table {
	if deny == nil {
		deny = DenyAPI
	}
	return &Table{Deps: deps, Mux: mux, Deny: deny}
}

// Handle registers pattern with its access and handler. `{group}` and `{tx}`
// are resolved before the handler runs.
func (t *Table) Handle(pattern string, access Access, h Handler) {
	t.Mux.HandleFunc(pattern, t.Serve(access, h))
}

// Serve is the dispatcher as a plain handler.
func (t *Table) Serve(access Access, h Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !SameOriginUnsafe(r) {
			t.Deny(w, r, CrossOrigin)
			return
		}
		sc, d, err := t.resolve(w, r, access)
		if err != nil {
			t.Deps.WriteError(w, r, err)
			return
		}
		if d != 0 {
			t.Deny(w, r, d)
			return
		}
		if err := h(w, r, sc); err != nil {
			t.Deps.WriteError(w, r, err)
		}
	}
}

func (t *Table) resolve(w http.ResponseWriter, r *http.Request, access Access) (Scope, Denial, error) {
	var sc Scope
	if access > Public {
		sc.User, sc.HasUser = t.Deps.Session(w, r)
		if !sc.HasUser {
			return sc, NoSession, nil
		}
	}

	if raw := r.PathValue("tx"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return sc, NoGroup, nil
		}
		sc.TxID = id
		groupID, err := t.Deps.GroupOfTransaction(r.Context(), id)
		if err != nil {
			return sc, 0, err
		}
		if groupID == 0 {
			return sc, NoGroup, nil
		}
		sc.GroupID = groupID
	}
	if raw := r.PathValue("group"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return sc, NoGroup, nil
		}
		// A route carrying both must agree: a Transaction reached through
		// another Group's URL is not that Group's business.
		if sc.GroupID != 0 && sc.GroupID != id {
			return sc, NoGroup, nil
		}
		sc.GroupID = id
	}

	if access < Member || sc.GroupID == 0 {
		return sc, 0, nil
	}
	member, owner, err := t.Deps.Membership(r.Context(), sc.GroupID, sc.User.UserID)
	if err != nil {
		return sc, 0, err
	}
	sc.IsOwner = owner
	if !member {
		// A Group somebody is not in and a Group that does not exist are the
		// same answer on purpose: the URL is a guessable integer, and a 403
		// would say whether it names a real Group.
		return sc, NotMember, nil
	}
	if access == Owner && !owner {
		return sc, NotOwner, nil
	}
	return sc, 0, nil
}

// DenyAPI writes a refusal as a status the page's fetch can read.
func DenyAPI(w http.ResponseWriter, r *http.Request, d Denial) {
	switch d {
	case NoSession:
		http.Error(w, "not authenticated", http.StatusUnauthorized)
	case NoGroup, NotMember:
		http.Error(w, "not found", http.StatusNotFound)
	case NotOwner:
		http.Error(w, "forbidden", http.StatusForbidden)
	default:
		http.Error(w, "forbidden", http.StatusForbidden)
	}
}

// DenyPage sends a logged-out visitor to /login carrying where they were
// going, so an invitee who followed an Invite Link lands back on it.
func DenyPage(w http.ResponseWriter, r *http.Request, d Denial) {
	if d == NoSession {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return
	}
	DenyAPI(w, r, d)
}

// SameOriginUnsafe is the check every state-changing request passes: a present
// Origin header must name this very host. The session cookie is SameSite=Lax,
// so a cross-site POST would not carry it anyway; this is the second lock.
func SameOriginUnsafe(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}

// Status is an error carrying the status and the words to answer with. A
// handler returns one; the app's WriteError writes it.
type Status struct {
	Code int
	Msg  string
}

func (e *Status) Error() string { return e.Msg }

// Errorf-free constructors for the three refusals handlers reach for.
func NotFound(msg string) error  { return &Status{Code: http.StatusNotFound, Msg: msg} }
func Forbidden(msg string) error { return &Status{Code: http.StatusForbidden, Msg: msg} }
func Conflict(msg string) error  { return &Status{Code: http.StatusConflict, Msg: msg} }

// AsStatus reports the status an error carries, if it carries one.
func AsStatus(err error) (*Status, bool) {
	var s *Status
	ok := errors.As(err, &s)
	return s, ok
}
