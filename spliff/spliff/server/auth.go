package spliffserver

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"pecheny.me/dopecore/authcred"
	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopecore/session"
	"pecheny.me/dopecore/tglogin"

	"spliff/spliff/web/route"

	spliffstrings "spliff/i18nstrings"
)

// Registration is open through the shared Telegram handshake, and a password
// account is what the /admin page and the `adduser` command mint. The state
// machine is dopecore/tglogin (root docs/adr/0004); this file is Spliff's
// adapter — its write transaction, its users table and its words.

// spliffSessions is authcred's session store over Spliff's users table.
var spliffSessions = authcred.Sessions{
	UserColumns: "u.username, u.telegram_username",
	UserDest:    func(u *session.User) ([]any, func()) { return []any{&u.Username, &u.Telegram}, nil },
}

// lookupSession resolves the session cookie to a user. An expired session is
// deleted; a live one slides, with the browser cookie, so a Member who uses
// Spliff every week is never logged out.
func (s *server) lookupSession(w http.ResponseWriter, r *http.Request) (session.User, bool) {
	c, err := r.Cookie(session.CookieName)
	if err != nil || c.Value == "" {
		return session.User{}, false
	}
	now := time.Now()
	u, state, slide := spliffSessions.Lookup(r.Context(), s.db, c.Value, now)
	switch state {
	case authcred.NoSession:
		return session.User{}, false
	case authcred.Expired:
		logDropped("session-expire", s.withWriteTx(r.Context(), "session-expire", func(ctx context.Context, tx *sql.Tx) error {
			return authcred.DeleteSession(ctx, tx, u.SessionID)
		}))
		return session.User{}, false
	}
	if slide {
		logDropped("session-refresh", s.withWriteTx(r.Context(), "session-refresh", func(ctx context.Context, tx *sql.Tx) error {
			return authcred.SlideSession(ctx, tx, u.SessionID, now)
		}))
		session.SetCookie(w, c.Value)
	}
	return u, true
}

// usernameRe is the shape of a username: letters, digits and ._- . The length
// is checked separately so the two refusals can say different things.
var usernameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validNewUsername(name string) bool {
	return len(name) >= 3 && len(name) <= 64 && usernameRe.MatchString(name)
}

// ---- what the login page asks ----

type meResponse struct {
	UserID   int64   `json:"user_id"`
	Username *string `json:"username"`
	Telegram *string `json:"telegram"`
}

func meOf(u session.User) meResponse {
	out := meResponse{UserID: u.UserID}
	if u.Username.Valid {
		out.Username = &u.Username.String
	}
	if u.Telegram.Valid {
		out.Telegram = &u.Telegram.String
	}
	return out
}

func (s *server) handleMe(w http.ResponseWriter, _ *http.Request, sc route.Scope) error {
	return writeJSON(w, meOf(sc.User))
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	if sc.HasUser {
		logDropped("logout", s.withWriteTx(r.Context(), "logout", func(ctx context.Context, tx *sql.Tx) error {
			return authcred.DeleteSession(ctx, tx, sc.User.SessionID)
		}))
	}
	session.ClearCookie(w)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// The three answers the login page can get about telegram. Being configured and
// being usable are different things, and a visitor deserves to know which one
// failed rather than watching a code no bot will ever collect.
const (
	tgStatusOK            = "ok"
	tgStatusMisconfigured = "misconfigured"
	tgStatusUnreachable   = "unreachable"
)

// botUsername is the login bot's @handle, which the t.me deep link is built
// from. Required for telegram login: a token without a handle advertises a way
// in and then shows a dead link.
func botUsername() string { return strings.TrimSpace(os.Getenv("SPLIFF_BOT_USERNAME")) }

func (s *server) telegramStatus() string {
	if s.bot == nil || botUsername() == "" {
		return tgStatusMisconfigured
	}
	if !s.botPolling() {
		return tgStatusUnreachable
	}
	return tgStatusOK
}

func (s *server) handleLoginMethods(w http.ResponseWriter, _ *http.Request, _ route.Scope) error {
	st := s.telegramStatus()
	return writeJSON(w, map[string]any{"telegram": st == tgStatusOK, "telegram_status": st})
}

// ---- the telegram handshake ----

// The handshake carries Accounts as well as Users: /profile links a telegram
// onto the account a person is already in, and that is the one step that has to
// read the account it is about.
func (s *server) handshake() tglogin.Handshake {
	return tglogin.Handshake{Users: spliffUsers{}, Accounts: spliffUsers{}}
}

type tgStartResponse struct {
	Code        string `json:"code"`
	ExpiresAt   string `json:"expires_at"`
	BotUsername string `json:"bot_username,omitempty"`
}

func (s *server) handleTgStart(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	if s.bot == nil {
		return &route.Status{
			Code: http.StatusServiceUnavailable,
			Msg:  spliffstrings.Default.Auth.Tg.NotConfigured(),
		}
	}
	var out tgStartResponse
	err := s.withWriteTx(r.Context(), "tg-start", func(ctx context.Context, tx *sql.Tx) error {
		res, err := s.handshake().Start(ctx, tx, time.Now())
		out = tgStartResponse{Code: res.Code, ExpiresAt: rfc3339(res.ExpiresAt), BotUsername: botUsername()}
		return err
	})
	if err != nil {
		return err
	}
	return writeJSON(w, out)
}

type tgStatusResponse struct {
	Status   string  `json:"status"`
	Username *string `json:"username,omitempty"`
}

func (s *server) handleTgStatus(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		return corei18n.User(spliffstrings.Default.Auth.Tg.CodeMissing())
	}
	var out tglogin.Outcome
	err := s.withWriteTx(r.Context(), "tg-status", func(ctx context.Context, tx *sql.Tx) (err error) {
		out, err = s.handshake().Resolve(ctx, tx, code, time.Now())
		return err
	})
	return writeOutcome(w, out, err)
}

func (s *server) handleTgClaim(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	var req struct {
		Code     string `json:"code"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	name := strings.TrimSpace(req.Username)
	if !validNewUsername(name) {
		return corei18n.User(spliffstrings.Default.Auth.Username.Format())
	}
	var out tglogin.Outcome
	err := s.withWriteTx(r.Context(), "tg-claim", func(ctx context.Context, tx *sql.Tx) (err error) {
		out, err = s.handshake().Claim(ctx, tx, req.Code, name, req.Password, time.Now())
		str := spliffstrings.Default
		switch {
		case errors.Is(err, tglogin.ErrCodeNotFound):
			return corei18n.User(str.Auth.Tg.CodeMissing())
		case errors.Is(err, tglogin.ErrWrongPassword):
			return corei18n.User(str.Auth.Login.Invalid())
		case errors.Is(err, tglogin.ErrTelegramLinked):
			return corei18n.User(str.Auth.Tg.TelegramTaken())
		}
		return err
	})
	return writeOutcome(w, out, err)
}

func writeOutcome(w http.ResponseWriter, out tglogin.Outcome, err error) error {
	if err != nil {
		return err
	}
	if out.Token != "" {
		session.SetCookie(w, out.Token)
	}
	return writeJSON(w, tgStatusResponse{Status: out.Status, Username: out.Username})
}

// spliffUsers is Spliff's users table as the handshake needs it.
type spliffUsers struct{}

// accountColumns is the users row as the handshake reads it, in Account's own
// field order, so one scan serves all three lookups.
const accountColumns = `id, username, password_hash, password_salt, telegram_user_id`

func (spliffUsers) ByTelegram(ctx context.Context, tx tglogin.Tx, tg int64) (tglogin.Account, bool, error) {
	return scanAccount(tx.QueryRowContext(ctx,
		`select `+accountColumns+` from users where telegram_user_id = ?`, tg))
}

func (spliffUsers) ByUsername(ctx context.Context, tx tglogin.Tx, username string) (tglogin.Account, bool, error) {
	return scanAccount(tx.QueryRowContext(ctx,
		`select `+accountColumns+` from users where username = ?`, username))
}

func (spliffUsers) ByID(ctx context.Context, tx tglogin.Tx, userID int64) (tglogin.Account, bool, error) {
	return scanAccount(tx.QueryRowContext(ctx,
		`select `+accountColumns+` from users where id = ?`, userID))
}

func scanAccount(row *sql.Row) (tglogin.Account, bool, error) {
	var a tglogin.Account
	switch err := row.Scan(&a.ID, &a.Username, &a.PasswordHash, &a.PasswordSalt, &a.TelegramUserID); {
	case errors.Is(err, sql.ErrNoRows):
		return a, false, nil
	case err != nil:
		return a, false, err
	}
	return a, true, nil
}

func (spliffUsers) Create(ctx context.Context, tx tglogin.Tx, id tglogin.Identity, username string, now time.Time) (int64, error) {
	if isAdminUsername(username) {
		return 0, route.Forbidden(spliffstrings.Default.Auth.Username.Reserved())
	}
	res, err := tx.ExecContext(ctx, `
insert into users(telegram_user_id, telegram_username, telegram_name, username, created_at, updated_at)
values(?, ?, ?, ?, ?, ?)`, id.TelegramUserID, id.Username, id.Name, username, rfc3339(now), rfc3339(now))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (spliffUsers) Attach(ctx context.Context, tx tglogin.Tx, userID int64, id tglogin.Identity, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
update users set telegram_user_id = ?, telegram_username = ?, telegram_name = ?, updated_at = ? where id = ?`,
		id.TelegramUserID, id.Username, id.Name, rfc3339(now), userID)
	return err
}

// ---- password login ----

func (s *server) handleLoginPassword(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	str := spliffstrings.Default
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	name := strings.TrimSpace(req.Username)
	if name == "" || req.Password == "" {
		return corei18n.User(str.Auth.Login.FieldsRequired())
	}
	now := time.Now()
	var (
		token string
		me    meResponse
	)
	err := s.withWriteTx(r.Context(), "login-password", func(ctx context.Context, tx *sql.Tx) error {
		var uid int64
		var hash, salt, username, telegram sql.NullString
		row := tx.QueryRowContext(ctx,
			`select id, password_hash, password_salt, username, telegram_username from users where username = ?`, name)
		if err := row.Scan(&uid, &hash, &salt, &username, &telegram); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return corei18n.User(str.Auth.Login.Invalid())
			}
			return err
		}
		ok, upgraded, err := authcred.VerifyPasswordUpgrading(hash.String, salt.String, req.Password)
		if err != nil {
			return err
		}
		if !ok {
			return corei18n.User(str.Auth.Login.Invalid())
		}
		if upgraded != "" {
			if _, err := tx.ExecContext(ctx,
				`update users set password_hash = ?, password_salt = null, updated_at = ? where id = ?`,
				upgraded, rfc3339(now), uid); err != nil {
				return err
			}
		}
		token, err = authcred.CreateSession(ctx, tx, uid, now)
		if err != nil {
			return err
		}
		me = meOf(session.User{UserID: uid, Username: username, Telegram: telegram})
		return nil
	})
	if err != nil {
		return err
	}
	session.SetCookie(w, token)
	return writeJSON(w, me)
}
