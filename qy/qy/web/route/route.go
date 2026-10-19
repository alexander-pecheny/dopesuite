// Package route is the one dispatcher behind qy's routers. A Table holds
// routes as data — a Go 1.22 mux pattern, the access the route asks of its
// caller, the handler — and resolves the session and the admin bit before
// the handler runs. A handler returns an error and the dispatcher writes it,
// so a status is decided in one place. It is spliff's web/route with the
// Group checks swapped for one admin check; whether somebody is a juror
// depends on the year, so the handlers that need it ask.
package route

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"

	"code.pecheny.me/pecheny/dopesuite/dopecore/sameorigin"
	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
)

// Access is what a route asks of its caller.
type Access int

const (
	Public Access = iota
	LoggedIn
	Admin
)

// Scope is what the dispatcher resolved before calling the handler.
type Scope struct {
	User    session.User
	HasUser bool
	IsAdmin bool
}

type Handler func(w http.ResponseWriter, r *http.Request, sc Scope) error

// Denial is why the dispatcher refused a request before the handler ran.
type Denial int

const (
	NoSession Denial = iota + 1
	NotAdmin
	CrossOrigin
)

type Deps struct {
	// Session resolves the request's cookie. It may write to w (the sliding
	// session's refreshed cookie), which is why it is handed one.
	Session func(w http.ResponseWriter, r *http.Request) (session.User, bool)
	IsAdmin func(ctx context.Context, u session.User) (bool, error)
	// WriteError turns a handler's error into a response.
	WriteError func(w http.ResponseWriter, r *http.Request, err error)
}

// Table is a set of routes sharing one refusal policy.
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
		var sc Scope
		sc.User, sc.HasUser = t.Deps.Session(w, r)
		if access > Public && !sc.HasUser {
			t.Deny(w, r, NoSession)
			return
		}
		if sc.HasUser {
			admin, err := t.Deps.IsAdmin(r.Context(), sc.User)
			if err != nil {
				t.Deps.WriteError(w, r, err)
				return
			}
			sc.IsAdmin = admin
		}
		if access == Admin && !sc.IsAdmin {
			t.Deny(w, r, NotAdmin)
			return
		}
		if err := h(w, r, sc); err != nil {
			t.Deps.WriteError(w, r, err)
		}
	}
}

// DenyAPI writes a refusal as a status the page's fetch can read.
func DenyAPI(w http.ResponseWriter, _ *http.Request, d Denial) {
	if d == NoSession {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	http.Error(w, "forbidden", http.StatusForbidden)
}

// DenyPage sends a logged-out visitor to /login carrying where they were
// going.
func DenyPage(w http.ResponseWriter, r *http.Request, d Denial) {
	if d == NoSession {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return
	}
	DenyAPI(w, r, d)
}

// TrustedOriginHostsEnv names the extra hosts whose Origin passes on a write.
const TrustedOriginHostsEnv = "QY_TRUSTED_ORIGIN_HOSTS"

// SameOriginUnsafe is the check every state-changing request passes
// (dopecore/sameorigin): a present Origin header must name this very host or a
// trusted one.
func SameOriginUnsafe(r *http.Request) bool {
	return sameorigin.Allowed(r, os.Getenv(TrustedOriginHostsEnv))
}

// Status is an error carrying the status and the words to answer with.
type Status struct {
	Code int
	Msg  string
}

func (e *Status) Error() string { return e.Msg }

func NotFound(msg string) error  { return &Status{Code: http.StatusNotFound, Msg: msg} }
func Forbidden(msg string) error { return &Status{Code: http.StatusForbidden, Msg: msg} }
func Conflict(msg string) error  { return &Status{Code: http.StatusConflict, Msg: msg} }

func AsStatus(err error) (*Status, bool) {
	var s *Status
	ok := errors.As(err, &s)
	return s, ok
}
