package dopeserver

import (
	"context"
	"database/sql"
	"dope/dope/domain/core"
	"dope/dope/platform/util"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"code.pecheny.me/pecheny/dopesuite/dopecore/adminusers"
	"code.pecheny.me/pecheny/dopesuite/dopecore/authcred"
	"code.pecheny.me/pecheny/dopesuite/dopecore/authweb"
	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
	"code.pecheny.me/pecheny/dopesuite/dopecore/tglogin"

	"dope/dope/storage/festaccess"
	"dope/dope/storage/store"
	"dope/dope/web/route"
	dopestrings "dope/i18nstrings"
	"strconv"
)

const (
	trustedOriginHostsEnv = route.TrustedOriginHostsEnv
	passwordMinLen        = 8
	// bcrypt only hashes the first 72 bytes of its input and rejects longer
	// passwords, so cap the new password at that boundary.
	passwordMaxLen = 72
)

type loginPasswordRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type usernameRequest struct {
	Username string `json:"username"`
}

type passwordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type meResponse struct {
	UserID   int64   `json:"user_id"`
	Username *string `json:"username,omitempty"`
	Telegram *string `json:"telegram,omitempty"`
}

type telegramSender func(ctx context.Context, chatID int64, text string) error

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// web is the shared login endpoints (dopecore/authweb) over dope's users
// table, its write transaction, its words and its route errors.
func (s *server) web() authweb.Web {
	str := dopestrings.Default
	return authweb.Web{
		Handshake: tglogin.Handshake{Users: dopeUsers{}},
		Passwords: dopePasswords{},
		WriteTx: func(ctx context.Context, _ string, fn func(ctx context.Context, tx *sql.Tx) error) error {
			return s.inWriteTx(ctx, func(tx *sql.Tx) error { return fn(ctx, tx) })
		},
		ReadJSON: route.DecodeJSON,
		Refuse:   func(code int, msg string) error { return &route.Status{Code: code, Msg: msg} },
		Me: func(ctx context.Context, tx *sql.Tx, id int64) (any, error) {
			u, err := loadUserTx(ctx, tx, id)
			return meResponseFor(u), err
		},
		HasBot:         func() bool { return strings.TrimSpace(s.getenv("TELEGRAM_BOT_TOKEN")) != "" },
		TelegramStatus: s.telegramStatus,
		BotUsername:    botUsername,
		ValidUsername:  util.ValidUsername,
		Words: authweb.Words{
			NotConfigured:   str.Auth.Login.TgNotConfigured(),
			CodeMissing:     str.Auth.Login.CodeMissing(),
			CodeNotFound:    str.Auth.Login.CodeNotFound(),
			UsernameInvalid: str.Auth.Login.UsernameInvalid(),
			PasswordWrong:   str.Auth.Login.PasswordWrong(),
			TelegramTaken:   str.Auth.Login.TelegramLinked(),
			FieldsRequired:  str.Auth.Login.CredentialsMissing(),
			LoginInvalid:    str.Auth.Login.CredentialsInvalid(),
		},
	}
}

// inWriteTx runs fn in one locked write transaction and commits it.
func (s *server) inWriteTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.eng.BeginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// The /api/auth table: every handler returns what it has to say, the
// dispatcher writes it. The exported HandleAuth* names in testapi.go reach
// the same handlers.
func (s *server) authRoutes(t *route.Table) {
	t.Handle("GET /api/auth/methods", route.Public, s.authLoginMethods)
	t.Handle("POST /api/auth/tg/start", route.Public, s.authTgStart)
	t.Handle("GET /api/auth/tg/status", route.Public, s.authTgStatus)
	t.Handle("POST /api/auth/tg/claim", route.Public, s.authTgClaim)
	t.Handle("POST /api/auth/login-password", route.Public, s.authLoginPassword)
	t.Handle("POST /api/auth/logout", route.Public, s.authLogout)
	t.Handle("GET /api/auth/me", route.Session, s.authMe)
	t.Handle("POST /api/auth/username", route.Session, s.authUsername)
	t.Handle("POST /api/auth/password", route.Session, s.authPassword)
}

func (s *server) handleAuthTgStart(w http.ResponseWriter, r *http.Request) {
	s.api().Serve(route.Public, s.authTgStart)(w, r)
}
func (s *server) handleAuthTgStatus(w http.ResponseWriter, r *http.Request) {
	s.api().Serve(route.Public, s.authTgStatus)(w, r)
}
func (s *server) handleAuthTgClaim(w http.ResponseWriter, r *http.Request) {
	s.api().Serve(route.Public, s.authTgClaim)(w, r)
}
func (s *server) handleAuthLoginPassword(w http.ResponseWriter, r *http.Request) {
	s.api().Serve(route.Public, s.authLoginPassword)(w, r)
}
func (s *server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	s.api().Serve(route.Session, s.authMe)(w, r)
}
func (s *server) handleAuthUsername(w http.ResponseWriter, r *http.Request) {
	s.api().Serve(route.Session, s.authUsername)(w, r)
}
func (s *server) handleAuthPassword(w http.ResponseWriter, r *http.Request) {
	s.api().Serve(route.Session, s.authPassword)(w, r)
}

// telegramStatus is whether this instance can log somebody in by telegram.
// No token means it was never meant to (staging); a token nobody is polling
// means the bot failed to start or another process holds it.
func (s *server) telegramStatus() string {
	return authweb.TelegramState(strings.TrimSpace(s.getenv("TELEGRAM_BOT_TOKEN")) != "", s.botPolling.Load())
}

func (s *server) authLoginMethods(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	return s.web().Methods(w, r)
}
func (s *server) authTgStart(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	return s.web().Start(w, r)
}
func (s *server) authTgStatus(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	return s.web().Status(w, r)
}
func (s *server) authTgClaim(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	return s.web().Claim(w, r)
}
func (s *server) authLoginPassword(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	return s.web().LoginPassword(w, r)
}

// getenv reads the process environment, unless the test seam has shadowed
// the key for this server (envOverride).
func (s *server) getenv(key string) string {
	if v, ok := s.envOverride[key]; ok {
		return v
	}
	return os.Getenv(key)
}

// botUsername is the login bot's @handle, used to build the t.me deep link the
// login page offers. DOPE_BOT_NAME overrides the default.
func botUsername() string {
	if v := strings.TrimSpace(os.Getenv("DOPE_BOT_NAME")); v != "" {
		return v
	}
	return "dope_pecheny_bot"
}

// dopeUsers is dope's users table as the handshake needs it: system accounts
// are invisible to it, so one can neither log in by telegram nor be claimed.
type dopeUsers struct{}

func (dopeUsers) ByTelegram(ctx context.Context, tx tglogin.Tx, tg int64) (tglogin.Account, bool, error) {
	return scanAccount(tx.QueryRowContext(ctx, `select id, username, password_hash, password_salt from users where telegram_user_id = ? and is_system = 0`, tg))
}

func (dopeUsers) ByUsername(ctx context.Context, tx tglogin.Tx, username string) (tglogin.Account, bool, error) {
	id, err := store.UserIDByName(ctx, tx, username)
	if errors.Is(err, sql.ErrNoRows) {
		return tglogin.Account{}, false, nil
	}
	if err != nil {
		return tglogin.Account{}, false, err
	}
	return scanAccount(tx.QueryRowContext(ctx, `
select id, username, password_hash, password_salt from users where id = ? and is_system = 0`, id))
}

func scanAccount(row *sql.Row) (tglogin.Account, bool, error) {
	var a tglogin.Account
	switch err := row.Scan(&a.ID, &a.Username, &a.PasswordHash, &a.PasswordSalt); {
	case errors.Is(err, sql.ErrNoRows):
		return a, false, nil
	case err != nil:
		return a, false, err
	}
	return a, true, nil
}

func (dopeUsers) Create(ctx context.Context, tx tglogin.Tx, id tglogin.Identity, username string, now time.Time) (int64, error) {
	if !adminusers.MayTake(festaccess.SiteAdminEnv, username, id.Username.String) {
		return 0, route.Conflict(dopestrings.Default.Auth.Username.Taken())
	}
	res, err := tx.ExecContext(ctx, `
insert into users(telegram_user_id, telegram_username, telegram_name, username, is_system, created_at, updated_at)
values(?, ?, ?, ?, 0, ?, ?)`, id.TelegramUserID, id.Username, id.Name, username, rfc3339(now), rfc3339(now))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (dopeUsers) Attach(ctx context.Context, tx tglogin.Tx, userID int64, id tglogin.Identity, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
update users set telegram_user_id = ?, telegram_username = ?, telegram_name = ?, updated_at = ? where id = ?`,
		id.TelegramUserID, id.Username, id.Name, rfc3339(now), userID)
	return err
}

// dopePasswords is dope's users table as password login reads it. A system
// account has a password but is not a person, and says so rather than
// answering "wrong password".
type dopePasswords struct{}

func (dopePasswords) ByUsername(ctx context.Context, tx tglogin.Tx, username string) (tglogin.Account, bool, error) {
	id, err := store.UserIDByName(ctx, tx, username)
	if errors.Is(err, sql.ErrNoRows) {
		return tglogin.Account{}, false, nil
	}
	if err != nil {
		return tglogin.Account{}, false, err
	}
	a := tglogin.Account{ID: id}
	var isSystem int
	err = tx.QueryRowContext(ctx, `select password_hash, password_salt, is_system from users where id = ?`, id).
		Scan(&a.PasswordHash, &a.PasswordSalt, &isSystem)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return a, false, nil
	case err != nil:
		return a, false, err
	case isSystem == 1 && a.PasswordHash.Valid:
		return a, false, route.Forbid(dopestrings.Default.Auth.Login.SystemUser())
	}
	return a, true, nil
}

func (dopePasswords) SetPasswordHash(ctx context.Context, tx *sql.Tx, userID int64, hash string, _ time.Time) error {
	_, err := tx.ExecContext(ctx, `
update users set password_hash = ?, password_salt = null, updated_at = ? where id = ?`, hash, util.UtcNow(), userID)
	return err
}

func (s *server) authMe(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	return route.JSON(w, meResponseFor(sc.User))
}

func (s *server) authLogout(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	s.logoutSession(r)
	session.ClearCookie(w)
	session.ClearSiteData(w)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) logoutSession(r *http.Request) {
	if cookie, err := r.Cookie(session.CookieName); err == nil {
		hash := authcred.HashSessionToken(cookie.Value)
		_, _ = s.eng.DB.ExecContext(r.Context(), `delete from sessions where token_hash = ?`, hash)
	}
}

func (s *server) authUsername(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	if core.BearerToken(r) != "" {
		return route.Forbid(dopestrings.Default.Auth.Token.Forbidden())
	}
	user := sc.User
	if user.Username.Valid {
		return route.Conflict(dopestrings.Default.Auth.Username.AlreadySet())
	}
	var req usernameRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	username := strings.TrimSpace(req.Username)
	if !util.ValidUsername(username) {
		return route.BadRequest(dopestrings.Default.Auth.Username.Invalid())
	}
	res, err := s.eng.WriteExec(r.Context(), `
update users set username = ?, updated_at = ? where id = ? and username is null`,
		username, util.UtcNow(), user.UserID)
	if util.IsUniqueViolation(err) {
		return route.Conflict(dopestrings.Default.Auth.Username.Taken())
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return route.Conflict(dopestrings.Default.Auth.Username.AlreadySet())
	}
	user.Username = sql.NullString{String: username, Valid: true}
	return route.JSON(w, meResponseFor(user))
}

// authPassword sets a password for the logged-in user, or changes an existing
// one — then the caller proves the current password first.
//
// Changing the password is the kill switch (ADR-0021): it revokes every API
// token of the account and ends every other session, which is why a token may
// not call it.
func (s *server) authPassword(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	if core.BearerToken(r) != "" {
		return route.Forbid(dopestrings.Default.Auth.Token.Forbidden())
	}
	var req passwordRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := checkNewPasswordLen(req.NewPassword); err != nil {
		return err
	}
	err := s.inWriteTx(r.Context(), func(tx *sql.Tx) error {
		return changePasswordTx(r.Context(), tx, sc.User, req)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// checkNewPasswordLen refuses a new password outside the allowed length.
func checkNewPasswordLen(password string) error {
	if len(password) < passwordMinLen {
		return route.BadRequest(dopestrings.Default.Auth.Password.TooShort(strconv.Itoa(passwordMinLen)))
	}
	if len(password) > passwordMaxLen {
		return route.BadRequest(dopestrings.Default.Auth.Password.TooLong(strconv.Itoa(passwordMaxLen)))
	}
	return nil
}

// changePasswordTx checks the current password, if the user has one, sets the
// new one, and signs out every other session and API token of the user.
func changePasswordTx(ctx context.Context, tx *sql.Tx, user session.User, req passwordRequest) error {
	var hash, salt sql.NullString
	if err := tx.QueryRowContext(ctx, `
select password_hash, password_salt from users where id = ?`, user.UserID).Scan(&hash, &salt); err != nil {
		return err
	}
	if hash.Valid && hash.String != "" {
		ok, _, err := authcred.VerifyPasswordUpgrading(hash.String, salt.String, req.CurrentPassword)
		if err != nil {
			return err
		}
		if !ok {
			return route.Unauthorized(dopestrings.Default.Auth.Password.CurrentWrong())
		}
	}
	hashed, err := authcred.HashPassword(req.NewPassword)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
update users set password_hash = ?, password_salt = null, updated_at = ? where id = ?`,
		hashed, util.UtcNow(), user.UserID); err != nil {
		return err
	}
	if err := core.RevokeAllAPITokensTx(ctx, tx, user.UserID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `delete from sessions where user_id = ? and id <> ?`, user.UserID, user.SessionID)
	return err
}

func loadUserTx(ctx context.Context, tx *sql.Tx, userID int64) (session.User, error) {
	var (
		username sql.NullString
		tgUser   sql.NullString
		isSystem int
	)
	err := tx.QueryRowContext(ctx, `
select username, telegram_username, is_system from users where id = ?`, userID).Scan(&username, &tgUser, &isSystem)
	if err != nil {
		return session.User{}, err
	}
	return session.User{
		UserID:   userID,
		Username: username,
		Telegram: tgUser,
		IsSystem: isSystem == 1,
	}, nil
}

func createSessionTx(ctx context.Context, tx *sql.Tx, userID int64, now time.Time) (string, error) {
	return authcred.CreateSession(ctx, tx, userID, now)
}

func meResponseFor(user session.User) meResponse {
	resp := meResponse{UserID: user.UserID}
	if user.Username.Valid {
		v := user.Username.String
		resp.Username = &v
	}
	if user.Telegram.Valid {
		v := user.Telegram.String
		resp.Telegram = &v
	}
	return resp
}

// RequireSameOriginUnsafe is route.SameOriginUnsafe, kept under the name the
// tests know.
func RequireSameOriginUnsafe(w http.ResponseWriter, r *http.Request) bool {
	return route.SameOriginUnsafe(w, r)
}

// inviteCodeAttempts is how many random codes createInvite tries before it
// gives up on finding one that is not taken.
const inviteCodeAttempts = 3

// createInvite is a small helper used by tests / future admin tooling. Not
// wired to an HTTP handler yet — invites are seeded out-of-band.
func createInvite(ctx context.Context, db *sql.DB, createdBy int64) (string, error) {
	now := time.Now().UTC()
	expires := now.Add(inviteLifetime).Format(time.RFC3339)
	for attempt := 0; attempt < inviteCodeAttempts; attempt++ {
		code, err := authcred.NewInviteCode()
		if err != nil {
			return "", err
		}
		_, err = db.ExecContext(ctx, `
insert into invites(code, created_by, created_at, expires_at)
values(?, ?, ?, ?)`, code, createdBy, now.Format(time.RFC3339), expires)
		if err == nil {
			return code, nil
		}
		if !util.IsUniqueViolation(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("could not allocate invite code")
}
