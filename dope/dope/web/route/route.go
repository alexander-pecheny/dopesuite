// Package route is the one dispatcher behind dope's routers. A Table holds
// routes as data — a Go 1.22 mux pattern, the access the route asks of its
// caller, the handler — and resolves what every handler used to resolve by
// hand: the fest and game named in the path, the session, the caller's role on
// the fest, the numbering guard. A handler takes the resolved Scope and returns
// an error; the dispatcher writes it, so a status is decided in one place.
package route

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/domain/numbering"
	"dope/dope/platform/roles"
	"dope/dope/storage/festaccess"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopecore/session"
)

// Access is what a route asks of its caller, from nothing to the fest's creator.
// Read is the viewer rule: a public fest, or any role on it. PublicFest is the
// viewer page rule: the fest itself must be public. Member is the host-page
// baseline: a session and any role on the fest.
type Access struct {
	level    level
	numbered bool
}

type level int

const (
	levelPublic level = iota
	levelSession
	levelPublicFest
	levelRead
	levelMember
	levelEditor
	levelManager
	levelCreator
)

var (
	Public     = Access{level: levelPublic}
	Session    = Access{level: levelSession}
	PublicFest = Access{level: levelPublicFest}
	Read       = Access{level: levelRead}
	Member     = Access{level: levelMember}
	Editor     = Access{level: levelEditor}
	Manager    = Access{level: levelManager}
	Creator    = Access{level: levelCreator}
)

// Numbered adds the numbering guard (CONTEXT.md): the route's Game must have
// every Participant numbered before the write is allowed (409 otherwise).
func (a Access) Numbered() Access { a.numbered = true; return a }

// Scope is what the dispatcher resolved before calling the handler. GameID is
// 0 on routes without a {game} segment; User is zero-valued without a session.
type Scope struct {
	FestID, GameID int64
	User           session.User
	HasUser        bool
	Role           string
}

// Fest is the Scope as the rest of the server names it.
func (sc Scope) Fest() core.FestScope { return core.FestScope{FestID: sc.FestID, GameID: sc.GameID} }

// Handler serves one route. An error it returns is written by the dispatcher —
// a *Status as its status and text, sql.ErrNoRows as 404, anything else as 500.
type Handler func(w http.ResponseWriter, r *http.Request, sc Scope) error

// Denial is why the dispatcher refused a request before the handler ran.
type Denial int

const (
	NoSession  Denial = iota + 1
	NoRole            // a session, but no role on the fest
	Forbidden         // a role, but not the one the route asks
	NotPublic         // a viewer page on a fest that is not public
	Unnumbered        // the numbering guard
	NoFest
	NoGame
)

// Table is a set of routes sharing one denial policy. Deny writes the refusal;
// nil means DenyAPI.
type Table struct {
	Eng  *core.Engine
	Mux  *http.ServeMux
	Deny func(w http.ResponseWriter, r *http.Request, d Denial)
}

func New(eng *core.Engine, deny func(w http.ResponseWriter, r *http.Request, d Denial)) *Table {
	if deny == nil {
		deny = DenyAPI
	}
	return &Table{Eng: eng, Mux: http.NewServeMux(), Deny: deny}
}

// Handle registers pattern (a mux pattern; {fest} and {game} are resolved,
// {rest...} is left to the handler) with its access and handler.
func (t *Table) Handle(pattern string, access Access, h Handler) {
	t.Mux.HandleFunc(pattern, t.serve(strings.Contains(pattern, "{fest}"), strings.Contains(pattern, "{game}"), access, h))
}

// Serve is the dispatcher as a plain handler, for a route whose fest is not in
// the path (or none): the access check, then h, then its error.
func (t *Table) Serve(access Access, h Handler) http.HandlerFunc {
	return t.serve(false, false, access, h)
}

func (t *Table) serve(hasFest, hasGame bool, access Access, h Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !SameOriginUnsafe(w, r) {
			return
		}
		var sc Scope
		var err error
		if hasFest {
			if sc.FestID, err = store.ResolveFestID(r.Context(), t.Eng.DB, r.PathValue("fest")); err != nil || sc.FestID <= 0 {
				t.refuse(w, r, err, NoFest)
				return
			}
		}
		if hasGame {
			if sc.GameID, err = ResolveGameID(r.Context(), t.Eng.DB, sc.FestID, r.PathValue("game")); err != nil || sc.GameID <= 0 {
				t.refuse(w, r, err, NoGame)
				return
			}
		}
		if d, err := t.admit(r, access, &sc); err != nil {
			WriteError(w, r, err)
			return
		} else if d != 0 {
			t.Deny(w, r, d)
			return
		}
		if err := h(w, r, sc); err != nil {
			WriteError(w, r, err)
		}
	}
}

// Admit runs the access check for a request whose fest is not in the path (the
// SSE endpoints name it in the query). It writes the refusal itself and
// reports whether the handler may proceed.
func (t *Table) Admit(w http.ResponseWriter, r *http.Request, access Access, festID, gameID int64) (Scope, bool) {
	sc := Scope{FestID: festID, GameID: gameID}
	d, err := t.admit(r, access, &sc)
	if err != nil {
		WriteError(w, r, err)
		return sc, false
	}
	if d != 0 {
		t.Deny(w, r, d)
		return sc, false
	}
	return sc, true
}

func (t *Table) refuse(w http.ResponseWriter, r *http.Request, err error, d Denial) {
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		WriteError(w, r, err)
		return
	}
	t.Deny(w, r, d)
}

// admit resolves the session and the role as far as the access needs, and
// answers the first reason to refuse.
func (t *Table) admit(r *http.Request, access Access, sc *Scope) (Denial, error) {
	ctx := r.Context()
	if access.level >= levelSession && access.level != levelPublicFest {
		sc.User, sc.HasUser = t.Eng.LookupSession(r)
	}
	switch access.level {
	case levelPublic:
	case levelSession:
		if !sc.HasUser {
			return NoSession, nil
		}
	case levelPublicFest:
		exists, public, err := festVisibility(ctx, t.Eng.DB, sc.FestID)
		if err != nil {
			return 0, err
		}
		if !exists {
			return NoFest, nil
		}
		if !public {
			return NotPublic, nil
		}
	case levelRead:
		exists, public, err := festVisibility(ctx, t.Eng.DB, sc.FestID)
		if err != nil {
			return 0, err
		}
		if !exists {
			return NoFest, nil
		}
		if public {
			break
		}
		// A private fest is invisible to anyone without a role on it.
		if !sc.HasUser {
			return NoFest, nil
		}
		if sc.Role, err = festaccess.FestUserRoleFromQuery(ctx, t.Eng.DB, sc.FestID, sc.User.UserID); err != nil {
			return 0, err
		}
		if sc.Role == "" {
			return NoFest, nil
		}
	default:
		if !sc.HasUser {
			return NoSession, nil
		}
		var err error
		if sc.Role, err = festaccess.FestUserRoleFromQuery(ctx, t.Eng.DB, sc.FestID, sc.User.UserID); err != nil {
			return 0, err
		}
		if !allowed(access.level, sc.Role) {
			exists, _, err := festVisibility(ctx, t.Eng.DB, sc.FestID)
			if err != nil {
				return 0, err
			}
			if !exists {
				return NoFest, nil
			}
			if sc.Role == "" {
				return NoRole, nil
			}
			return Forbidden, nil
		}
	}
	if access.numbered {
		blocked, err := numbering.GameHasUnnumbered(ctx, t.Eng.DB, sc.FestID, sc.GameID)
		if err != nil {
			return 0, err
		}
		if blocked {
			return Unnumbered, nil
		}
	}
	return 0, nil
}

func allowed(l level, role string) bool {
	switch l {
	case levelMember:
		return role != ""
	case levelEditor:
		return roles.CanEditGameTables(role)
	case levelManager:
		return roles.CanManageFest(role)
	case levelCreator:
		return roles.CanDeleteFest(role)
	}
	return true
}

// ErrUnnumbered is the numbering guard's refusal, in the host's words.
var ErrUnnumbered = errors.New(dopestrings.Default.Route.Guard.Unnumbered())

// DenyAPI is the API tables' policy: no session is 401, no role or the wrong
// one 403, the numbering guard 409, and a missing fest or game — or a private
// fest to an outsider — 404.
func DenyAPI(w http.ResponseWriter, r *http.Request, d Denial) {
	switch d {
	case NoSession:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	case NoRole, Forbidden:
		http.Error(w, "forbidden", http.StatusForbidden)
	case Unnumbered:
		http.Error(w, ErrUnnumbered.Error(), http.StatusConflict)
	default:
		http.NotFound(w, r)
	}
}

func festVisibility(ctx context.Context, db *sql.DB, festID int64) (exists, public bool, err error) {
	if db == nil {
		return false, false, nil
	}
	var isPublic int
	err = db.QueryRowContext(ctx, `select is_public from fests where id = ?`, festID).Scan(&isPublic)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, isPublic == 1, nil
}

// ResolveGameID accepts a positive integer (the game id) or a slug and returns
// the numeric game id within the fest; sql.ErrNoRows when absent.
func ResolveGameID(ctx context.Context, q store.Queryer, festID int64, ref string) (int64, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return 0, sql.ErrNoRows
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil && id > 0 {
		var found int64
		if err := q.QueryRowContext(ctx, `select id from games where id = ? and fest_id = ?`, id, festID).Scan(&found); err != nil {
			return 0, err
		}
		return found, nil
	}
	var id int64
	if err := q.QueryRowContext(ctx, `select id from games where fest_id = ? and slug = ?`, festID, ref).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// GamePagePath says whether parts (after the fest: "game", the game ref, then
// the view) name a game page: the game itself; venues, stats, roster, and for
// hosts seed-import; matches/{code} and stage/{code}.
func GamePagePath(parts []string, host bool) bool {
	if len(parts) < 2 || parts[0] != "game" || parts[1] == "" {
		return false
	}
	if len(parts) == 2 {
		return true
	}
	switch parts[2] {
	case "venues", "stats", "roster":
		return len(parts) == 3
	case "seed-import":
		return host && len(parts) == 3
	case "matches", "stage":
		return len(parts) == 4 && parts[3] != ""
	}
	return false
}

// ---- the response vocabulary ----

// Status is an error that knows its HTTP status.
type Status struct {
	Code int
	Msg  string
}

func (e *Status) Error() string { return e.Msg }

func Statusf(code int, format string, args ...any) error {
	return &Status{Code: code, Msg: fmt.Sprintf(format, args...)}
}
func BadRequest(msg string) error   { return &Status{Code: http.StatusBadRequest, Msg: msg} }
func Unauthorized(msg string) error { return &Status{Code: http.StatusUnauthorized, Msg: msg} }
func Conflict(msg string) error     { return &Status{Code: http.StatusConflict, Msg: msg} }
func Forbid(msg string) error       { return &Status{Code: http.StatusForbidden, Msg: msg} }

// NotFound is the handler's 404; sql.ErrNoRows from a lookup reads the same.
var NotFound = &Status{Code: http.StatusNotFound, Msg: "not found"}

// WriteError writes err: a *Status as itself, a UserError as a 400 carrying
// its message (written for the person who caused it), sql.ErrNoRows as 404,
// and the rest as one generic line over a log entry (root docs/adr/0006 —
// the edge shows only what a person may read).
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var st *Status
	switch {
	case errors.As(err, &st):
		if st.Code == http.StatusNotFound {
			http.NotFound(w, r)
			return
		}
		http.Error(w, st.Msg, st.Code)
	case isUser(err):
		msg, _ := corei18n.AsUser(err)
		http.Error(w, msg, http.StatusBadRequest)
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
	default:
		log.Printf("internal error: %v", err)
		http.Error(w, dopestrings.Default.Server.Error.Internal(), http.StatusInternalServerError)
	}
}

// BadUser maps a request failure to a 400: a UserError's message verbatim
// (it was written for the person who caused the failure), anything else one
// generic line over a log entry (root docs/adr/0006).
func BadUser(err error) error {
	if msg, ok := corei18n.AsUser(err); ok {
		return BadRequest(msg)
	}
	log.Printf("bad request: %v", err)
	return BadRequest(dopestrings.Default.Server.Error.BadRequest())
}

func isUser(err error) bool {
	_, ok := corei18n.AsUser(err)
	return ok
}

// JSON writes v as the response body.
func JSON(w http.ResponseWriter, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return JSONBytes(w, data)
}

// JSONBytes writes already-encoded JSON.
func JSONBytes(w http.ResponseWriter, data []byte) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, err := w.Write(data)
	return err
}

// DecodeJSON reads the body into v; a malformed body is a 400 "bad json".
func DecodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return BadRequest("bad json")
	}
	return nil
}

// ---- same-origin ----

// TrustedOriginHostsEnv names extra hosts whose Origin is accepted on unsafe
// methods (a staging front in front of the app).
const TrustedOriginHostsEnv = "DOPE_TRUSTED_ORIGIN_HOSTS"

// SameOriginUnsafe is the CSRF check every unsafe request passes: SameSite=Lax
// on the cookie is the primary defence, this refuses a cross-origin form submit
// outright. Writes 403 and returns false when it fails.
func SameOriginUnsafe(w http.ResponseWriter, r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || !SameOriginHost(u.Host, r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

// SameOriginHost reports whether originHost is the request's own host or a
// trusted one.
func SameOriginHost(originHost string, r *http.Request) bool {
	return strings.EqualFold(originHost, r.Host) || TrustedOriginHost(originHost, os.Getenv(TrustedOriginHostsEnv))
}

func TrustedOriginHost(originHost, trustedHosts string) bool {
	for _, candidate := range strings.Split(trustedHosts, ",") {
		host := strings.TrimSpace(candidate)
		if host == "" {
			continue
		}
		if u, err := url.Parse(host); err == nil && u.Host != "" {
			host = u.Host
		}
		if strings.EqualFold(originHost, host) {
			return true
		}
	}
	return false
}
