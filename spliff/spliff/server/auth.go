package spliffserver

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"code.pecheny.me/pecheny/dopesuite/dopecore/adminusers"
	"code.pecheny.me/pecheny/dopesuite/dopecore/authcred"
	"code.pecheny.me/pecheny/dopesuite/dopecore/authweb"
	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
	"code.pecheny.me/pecheny/dopesuite/dopecore/tglogin"

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

// lookupSession resolves the session cookie to a user (authweb.CookieSession):
// a live session slides, so a Member who uses Spliff every week is never
// logged out.
func (s *server) lookupSession(w http.ResponseWriter, r *http.Request) (session.User, bool) {
	return authweb.CookieSession(w, r, spliffSessions, s.db, s.withWriteTx)
}

// validNewUsername is the shared rule (authweb.ValidUsername).
func validNewUsername(name string) bool { return authweb.ValidUsername(name) }

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
	return s.web().Logout(w, r, sc.User, sc.HasUser)
}

// botUsername is the login bot's @handle, which the t.me deep link is built
// from. Required for telegram login: a token without a handle advertises a way
// in and then shows a dead link.
func botUsername() string { return strings.TrimSpace(os.Getenv("SPLIFF_BOT_USERNAME")) }

// web is the shared login endpoints (dopecore/authweb) over Spliff's users
// table, its write transaction and its words. The handshake carries Accounts
// as well as Users: /profile links a telegram onto the account a person is
// already in.
func (s *server) web() authweb.Web {
	str := spliffstrings.Default
	return authweb.Web{
		Handshake: tglogin.Handshake{Users: spliffUsers{}, Accounts: spliffUsers{}},
		Passwords: spliffUsers{},
		WriteTx:   s.withWriteTx,
		ReadJSON:  readJSON,
		Refuse:    func(code int, msg string) error { return &route.Status{Code: code, Msg: msg} },
		Me: func(ctx context.Context, tx *sql.Tx, id int64) (any, error) {
			u := session.User{UserID: id}
			err := tx.QueryRowContext(ctx, `select username, telegram_username from users where id = ?`, id).Scan(&u.Username, &u.Telegram)
			return meOf(u), err
		},
		HasBot:         func() bool { return s.bot != nil },
		TelegramStatus: func() string { return authweb.TelegramState(s.bot != nil && botUsername() != "", s.botPolling()) },
		BotUsername:    botUsername,
		ValidUsername:  validNewUsername,
		Words: authweb.Words{
			NotConfigured:   str.Auth.Tg.NotConfigured(),
			CodeMissing:     str.Auth.Tg.CodeMissing(),
			CodeNotFound:    str.Auth.Tg.CodeMissing(),
			UsernameInvalid: str.Auth.Username.Format(),
			PasswordWrong:   str.Auth.Login.Invalid(),
			TelegramTaken:   str.Auth.Tg.TelegramTaken(),
			FieldsRequired:  str.Auth.Login.FieldsRequired(),
			LoginInvalid:    str.Auth.Login.Invalid(),
		},
	}
}

func (s *server) handshake() tglogin.Handshake { return s.web().Handshake }

func (s *server) handleLoginMethods(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	return s.web().Methods(w, r)
}
func (s *server) handleTgStart(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	return s.web().Start(w, r)
}
func (s *server) handleTgStatus(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	return s.web().Status(w, r)
}
func (s *server) handleTgClaim(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	return s.web().Claim(w, r)
}
func (s *server) handleLoginPassword(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	return s.web().LoginPassword(w, r)
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
	if !adminusers.MayTake(adminUserEnv, username, id.Username.String) {
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

func (spliffUsers) SetPasswordHash(ctx context.Context, tx *sql.Tx, userID int64, hash string, now time.Time) error {
	_, err := tx.ExecContext(ctx,
		`update users set password_hash = ?, password_salt = null, updated_at = ? where id = ?`, hash, rfc3339(now), userID)
	return err
}
