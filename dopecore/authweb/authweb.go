// Package authweb is the HTTP half of the login every app shares: the
// /api/auth endpoints the kit's login page (dopeuikit login.ts) talks to, the
// cookie-session lookup, and the pending-telegram-login cookie that lets ANY tab
// finish a login the bot has confirmed. tglogin is the state machine; this
// package is the one wire format over it. Each app brings its write
// transaction, its users table, its words and its error type, and mounts the
// handlers on its own router (they all return error, the way every app's
// handlers do).
package authweb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"code.pecheny.me/pecheny/dopesuite/dopecore/authcred"
	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
	"code.pecheny.me/pecheny/dopesuite/dopecore/tglogin"
)

// The /api/auth/methods answers about telegram. Configured and usable are
// different things, and a visitor deserves to know which one failed rather
// than watch a code no bot will ever collect.
const (
	TelegramOK            = "ok"
	TelegramMisconfigured = "misconfigured"
	TelegramUnreachable   = "unreachable"
)

// TelegramState is the one rule every app states its bot by: no bot (or no
// handle to build the t.me link from) is misconfigured, a bot nobody is
// polling for is unreachable.
func TelegramState(configured, polling bool) string {
	switch {
	case !configured:
		return TelegramMisconfigured
	case !polling:
		return TelegramUnreachable
	}
	return TelegramOK
}

// Words is what the endpoints say when they refuse. Every app has its own
// catalog, and some its own language.
type Words struct {
	NotConfigured   string // start on an instance with no bot
	CodeMissing     string // status/claim with no code and no pending login
	CodeNotFound    string // claim on a code that lapsed or never was
	UsernameInvalid string // claim with a username of the wrong shape
	PasswordWrong   string // claim proving a password account, wrong password
	TelegramTaken   string // claim linking a telegram somebody else has
	FieldsRequired  string // password login with an empty field
	LoginInvalid    string // password login, no such user or wrong password
}

// Tx is the transaction an app's WriteTx hands out.
type Tx = *sql.Tx

// Passwords is the app's users table as password login needs it. ByUsername
// answers found=false for nobody AND for an account that cannot log in with a
// password (no hash); a refusal of its own — dope's system accounts — it
// returns as an error, which passes through. SetPasswordHash stores the bcrypt
// hash a legacy hash was upgraded to on a successful login.
type Passwords interface {
	ByUsername(ctx context.Context, tx tglogin.Tx, username string) (tglogin.Account, bool, error)
	SetPasswordHash(ctx context.Context, tx Tx, userID int64, hash string, now time.Time) error
}

// Web is one app's login endpoints.
type Web struct {
	Handshake tglogin.Handshake
	Passwords Passwords

	// WriteTx runs fn in the app's locked write transaction and commits it.
	WriteTx func(ctx context.Context, label string, fn func(ctx context.Context, tx Tx) error) error
	// ReadJSON decodes a request body the app's way (size cap, unknown fields).
	ReadJSON func(r *http.Request, v any) error
	// Refuse is the app's error carrying an HTTP status and a message for the
	// person; its edge renders it.
	Refuse func(code int, msg string) error
	// Me is what a successful password login answers: the app's /api/auth/me
	// body for the account that just logged in.
	Me func(ctx context.Context, tx Tx, userID int64) (any, error)

	// HasBot is whether this instance runs a bot at all — the gate on Start.
	// It is looser than TelegramStatus: a code minted while the bot is down or
	// its handle unset keeps, and is answered once that is fixed.
	HasBot         func() bool
	TelegramStatus func() string
	BotUsername    func() string
	ValidUsername  func(string) bool
	Words          Words
}

func writeJSON(w http.ResponseWriter, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, err = w.Write(data)
	return err
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Methods answers which ways in this instance offers. `telegram` is the bare
// on/off an older page reads; `telegram_status` says which kind of no it is.
func (web Web) Methods(w http.ResponseWriter, _ *http.Request) error {
	st := web.TelegramStatus()
	return writeJSON(w, map[string]any{"telegram": st == TelegramOK, "telegram_status": st})
}

// StartResponse is /api/auth/tg/start's body.
type StartResponse struct {
	Code        string `json:"code"`
	ExpiresAt   string `json:"expires_at"`
	BotUsername string `json:"bot_username,omitempty"`
}

// StatusResponse is the body of /api/auth/tg/status and /claim. Code and
// BotUsername are only filled in when the status was asked for with no code,
// about the login this browser has pending: a tab that did not start the login
// learns the code it is about, to show it or claim with it.
type StatusResponse struct {
	Status      string  `json:"status"`
	Username    *string `json:"username,omitempty"`
	Code        string  `json:"code,omitempty"`
	BotUsername string  `json:"bot_username,omitempty"`
	// Telegram names the telegram a password_required claim would link.
	Telegram *string `json:"telegram,omitempty"`
}

// Start mints a code for the visitor to forward to the bot. An instance with
// no bot refuses: minting one would only leave the visitor polling. The bot not
// polling right now is different — the code keeps, and the bot may be back
// before it lapses.
//
// A visitor who is not logged in also gets the code as the pending-login
// cookie, so whichever tab of theirs comes back first can finish the login. A
// logged-in one is linking a telegram onto their account (spliff's /profile
// shares this endpoint), and a pending cookie there could log them into
// another account the telegram already belongs to.
func (web Web) Start(w http.ResponseWriter, r *http.Request) error {
	if !web.HasBot() {
		return web.Refuse(http.StatusServiceUnavailable, web.Words.NotConfigured)
	}
	var res tglogin.StartResult
	err := web.WriteTx(r.Context(), "tg-start", func(ctx context.Context, tx Tx) (err error) {
		res, err = web.Handshake.Start(ctx, tx, time.Now())
		return err
	})
	if err != nil {
		return err
	}
	if !session.HasCookie(r) {
		setPending(w, res.Code, res.ExpiresAt)
	}
	return writeJSON(w, StartResponse{Code: res.Code, ExpiresAt: rfc3339(res.ExpiresAt), BotUsername: web.BotUsername()})
}

// Status polls a code: a known telegram logs straight in ("ready", with the
// session cookie), a new one answers "choose_username" for Claim. With no
// ?code it is about this browser's pending login, and an empty "not_found"
// when there is none — the login page asks this on every load.
func (web Web) Status(w http.ResponseWriter, r *http.Request) error {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	resumed := false
	if code == "" {
		if code = PendingCode(r); code == "" {
			return writeJSON(w, StatusResponse{Status: tglogin.NotFound})
		}
		resumed = true
	}
	out, err := web.resolve(r.Context(), code)
	if err != nil {
		return err
	}
	settle(w, out)
	res := StatusResponse{Status: out.Status, Username: out.Username}
	if resumed && (out.Status == tglogin.Pending || out.Status == tglogin.ChooseUsername) {
		res.Code, res.BotUsername = tglogin.Normalise(code), web.BotUsername()
	}
	return writeJSON(w, res)
}

func (web Web) resolve(ctx context.Context, code string) (out tglogin.Outcome, err error) {
	err = web.WriteTx(ctx, "tg-status", func(ctx context.Context, tx Tx) (err error) {
		out, err = web.Handshake.Resolve(ctx, tx, code, time.Now())
		return err
	})
	return out, err
}

// Claim settles a brand-new telegram on a username, creating the account or —
// with its password — linking an existing one. The code is the body's, else
// the pending login's, so a tab that only resumed the login can claim it too.
func (web Web) Claim(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Code     string `json:"code"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := web.ReadJSON(r, &req); err != nil {
		return err
	}
	name := strings.TrimSpace(req.Username)
	if !web.ValidUsername(name) {
		return web.Refuse(http.StatusBadRequest, web.Words.UsernameInvalid)
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		code = PendingCode(r)
	}
	if code == "" {
		return web.Refuse(http.StatusBadRequest, web.Words.CodeMissing)
	}
	var out tglogin.Outcome
	err := web.WriteTx(r.Context(), "tg-claim", func(ctx context.Context, tx Tx) (err error) {
		out, err = web.Handshake.Claim(ctx, tx, code, name, req.Password, time.Now())
		switch {
		case errors.Is(err, tglogin.ErrCodeNotFound):
			return web.Refuse(http.StatusBadRequest, web.Words.CodeNotFound)
		case errors.Is(err, tglogin.ErrWrongPassword):
			return web.Refuse(http.StatusUnauthorized, web.Words.PasswordWrong)
		case errors.Is(err, tglogin.ErrTelegramLinked):
			return web.Refuse(http.StatusConflict, web.Words.TelegramTaken)
		}
		return err
	})
	if err != nil {
		return err
	}
	settle(w, out)
	return writeJSON(w, StatusResponse{Status: out.Status, Username: out.Username, Telegram: out.Telegram})
}

// settle writes the cookies an outcome implies: the session when one was
// minted, and the pending login gone once its code is spent or lapsed.
func settle(w http.ResponseWriter, out tglogin.Outcome) {
	if out.Token != "" {
		session.SetCookie(w, out.Token)
	}
	switch out.Status {
	case tglogin.Ready, tglogin.Linked, tglogin.Expired, tglogin.NotFound:
		clearPending(w)
	}
}

// LoginPassword logs an existing account in by username and password,
// upgrading a legacy hash to bcrypt on the way.
func (web Web) LoginPassword(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := web.ReadJSON(r, &req); err != nil {
		return err
	}
	name := strings.TrimSpace(req.Username)
	if name == "" || req.Password == "" {
		return web.Refuse(http.StatusBadRequest, web.Words.FieldsRequired)
	}
	var (
		token string
		me    any
	)
	err := web.WriteTx(r.Context(), "login-password", func(ctx context.Context, tx Tx) error {
		now := time.Now()
		acct, found, err := web.Passwords.ByUsername(ctx, tx, name)
		if err != nil {
			return err
		}
		if !found || !acct.PasswordHash.Valid || acct.PasswordHash.String == "" {
			return web.Refuse(http.StatusUnauthorized, web.Words.LoginInvalid)
		}
		ok, upgraded, err := authcred.VerifyPasswordUpgrading(acct.PasswordHash.String, acct.PasswordSalt.String, req.Password)
		if err != nil {
			return err
		}
		if !ok {
			return web.Refuse(http.StatusUnauthorized, web.Words.LoginInvalid)
		}
		if upgraded != "" {
			// Lazy migration: the weaker hash leaves the database on the
			// first login that proves the password.
			if err := web.Passwords.SetPasswordHash(ctx, tx, acct.ID, upgraded, now); err != nil {
				return err
			}
		}
		if token, err = authcred.CreateSession(ctx, tx, acct.ID, now); err != nil {
			return err
		}
		me, err = web.Me(ctx, tx, acct.ID)
		return err
	})
	if err != nil {
		return err
	}
	session.SetCookie(w, token)
	clearPending(w)
	return writeJSON(w, me)
}

// Logout ends the request's session, if it has one, and drops the cookies.
func (web Web) Logout(w http.ResponseWriter, r *http.Request, u session.User, hasUser bool) error {
	if hasUser {
		_ = web.WriteTx(r.Context(), "logout", func(ctx context.Context, tx Tx) error {
			return authcred.DeleteSession(ctx, tx, u.SessionID)
		})
	}
	session.ClearCookie(w)
	clearPending(w)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// CookieSession resolves the session cookie to a user. An expired session is
// deleted; a live one slides, with the browser cookie, so somebody who keeps
// coming back is never logged out. Failures to write either are only logged by
// WriteTx's caller: the answer is the same.
func CookieSession(w http.ResponseWriter, r *http.Request, sessions authcred.Sessions, db authcred.Queryer,
	writeTx func(ctx context.Context, label string, fn func(ctx context.Context, tx Tx) error) error,
) (session.User, bool) {
	c, err := r.Cookie(session.CookieName)
	if err != nil || c.Value == "" {
		return session.User{}, false
	}
	now := time.Now()
	u, state, slide := sessions.Lookup(r.Context(), db, c.Value, now)
	switch state {
	case authcred.NoSession:
		return session.User{}, false
	case authcred.Expired:
		_ = writeTx(r.Context(), "session-expire", func(ctx context.Context, tx Tx) error {
			return authcred.DeleteSession(ctx, tx, u.SessionID)
		})
		return session.User{}, false
	}
	if slide {
		_ = writeTx(r.Context(), "session-refresh", func(ctx context.Context, tx Tx) error {
			return authcred.SlideSession(ctx, tx, u.SessionID, now)
		})
		session.SetCookie(w, c.Value)
	}
	return u, true
}

// The shape of a new username in the apps that share it (qy, spliff, xy):
// 3–64 bytes of letters, digits and ._- . dope's is older and narrower
// (2–32) and stays its own.
const (
	UsernameMinLen = 3
	UsernameMaxLen = 64
)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ValidUsername reports whether name is a well-formed new username.
func ValidUsername(name string) bool {
	return len(name) >= UsernameMinLen && len(name) <= UsernameMaxLen && usernameRe.MatchString(name)
}
