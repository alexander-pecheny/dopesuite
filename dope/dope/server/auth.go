package dopeserver

import (
	"context"
	"database/sql"
	"dope/dope/platform/util"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"pecheny.me/dopecore/authcred"
	"pecheny.me/dopecore/session"
	"pecheny.me/dopecore/tglogin"

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

// The Telegram handshake (start → status poll → claim) is dopecore/tglogin's
// state machine over dope's users table; these handlers keep dope's write
// transaction, its validation and its error text.
func (s *server) handshake() tglogin.Handshake { return tglogin.Handshake{Users: dopeUsers{}} }

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

func (s *server) authTgStart(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	var res tglogin.StartResult
	err := s.inWriteTx(r.Context(), func(tx *sql.Tx) (err error) {
		res, err = s.handshake().Start(r.Context(), tx, time.Now())
		return err
	})
	if err != nil {
		return err
	}
	return route.JSON(w, session.StartRegisterResponse{Code: res.Code, ExpiresAt: rfc3339(res.ExpiresAt), BotUsername: botUsername()})
}

// botUsername is the login bot's @handle, used to build the t.me deep link the
// login page offers. DOPE_BOT_NAME overrides the default.
func botUsername() string {
	if v := strings.TrimSpace(os.Getenv("DOPE_BOT_NAME")); v != "" {
		return v
	}
	return "dope_pecheny_bot"
}

func (s *server) authTgStatus(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		return route.BadRequest(dopestrings.Default.Auth.Login.CodeMissing())
	}
	var out tglogin.Outcome
	err := s.inWriteTx(r.Context(), func(tx *sql.Tx) (err error) {
		out, err = s.handshake().Resolve(r.Context(), tx, code, time.Now())
		return err
	})
	return writeOutcome(w, out, err)
}

func (s *server) authTgClaim(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	var req struct {
		Code     string `json:"code"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	username := strings.TrimSpace(req.Username)
	if !util.ValidUsername(username) {
		return route.BadRequest(dopestrings.Default.Auth.Login.UsernameInvalid())
	}
	var out tglogin.Outcome
	err := s.inWriteTx(r.Context(), func(tx *sql.Tx) (err error) {
		out, err = s.handshake().Claim(r.Context(), tx, req.Code, username, req.Password, time.Now())
		switch {
		case errors.Is(err, tglogin.ErrCodeNotFound):
			return route.BadRequest(dopestrings.Default.Auth.Login.CodeNotFound())
		case errors.Is(err, tglogin.ErrWrongPassword):
			return route.Unauthorized(dopestrings.Default.Auth.Login.PasswordWrong())
		case errors.Is(err, tglogin.ErrTelegramLinked):
			return route.Conflict(dopestrings.Default.Auth.Login.TelegramLinked())
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
	return route.JSON(w, session.RegisterStatusResponse{Status: out.Status, Username: out.Username})
}

// dopeUsers is dope's users table as the handshake needs it: system accounts
// are invisible to it, so one can neither log in by telegram nor be claimed.
type dopeUsers struct{}

func (dopeUsers) ByTelegram(ctx context.Context, tx tglogin.Tx, tg int64) (tglogin.Account, bool, error) {
	return scanAccount(tx.QueryRowContext(ctx, `select id, username, password_hash, password_salt from users where telegram_user_id = ? and is_system = 0`, tg))
}

func (dopeUsers) ByUsername(ctx context.Context, tx tglogin.Tx, username string) (tglogin.Account, bool, error) {
	return scanAccount(tx.QueryRowContext(ctx, `select id, username, password_hash, password_salt from users where username = ? and is_system = 0`, username))
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

func (s *server) authLoginPassword(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	var req loginPasswordRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	username := strings.TrimSpace(req.Username)
	password := req.Password
	if username == "" || password == "" {
		return route.BadRequest(dopestrings.Default.Auth.Login.CredentialsMissing())
	}
	var user session.User
	var token string
	err := s.inWriteTx(r.Context(), func(tx *sql.Tx) error {
		ctx := r.Context()
		var (
			userID   int64
			hash     sql.NullString
			salt     sql.NullString
			isSystem int
		)
		err := tx.QueryRowContext(ctx, `
select id, password_hash, password_salt, is_system from users where username = ?`, username).Scan(
			&userID, &hash, &salt, &isSystem)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !hash.Valid) {
			return route.Unauthorized(dopestrings.Default.Auth.Login.CredentialsInvalid())
		}
		if err != nil {
			return err
		}
		if isSystem == 1 {
			return route.Forbid(dopestrings.Default.Auth.Login.SystemUser())
		}
		ok, upgraded, err := authcred.VerifyPasswordUpgrading(hash.String, salt.String, password)
		if err != nil {
			return err
		}
		if !ok {
			return route.Unauthorized(dopestrings.Default.Auth.Login.CredentialsInvalid())
		}
		if upgraded != "" {
			// Lazy migration: a legacy SHA256 hash becomes bcrypt on the first
			// successful login, so the weaker hash leaves the database.
			if _, err := tx.ExecContext(ctx, `
update users set password_hash = ?, password_salt = null, updated_at = ? where id = ?`,
				upgraded, util.UtcNow(), userID); err != nil {
				return err
			}
		}
		if token, err = createSessionTx(ctx, tx, userID, time.Now().UTC()); err != nil {
			return err
		}
		user, err = loadUserTx(ctx, tx, userID)
		return err
	})
	if err != nil {
		return err
	}
	session.SetCookie(w, token)
	return route.JSON(w, meResponseFor(user))
}

func (s *server) authMe(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	return route.JSON(w, meResponseFor(sc.User))
}

func (s *server) authLogout(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	s.logoutSession(r)
	session.ClearCookie(w)
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
func (s *server) authPassword(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req passwordRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	if len(req.NewPassword) < passwordMinLen {
		return route.BadRequest(dopestrings.Default.Auth.Password.TooShort(strconv.Itoa(passwordMinLen)))
	}
	if len(req.NewPassword) > passwordMaxLen {
		return route.BadRequest(dopestrings.Default.Auth.Password.TooLong(strconv.Itoa(passwordMaxLen)))
	}
	err := s.inWriteTx(r.Context(), func(tx *sql.Tx) error {
		ctx := r.Context()
		var hash, salt sql.NullString
		if err := tx.QueryRowContext(ctx, `
select password_hash, password_salt from users where id = ?`, sc.User.UserID).Scan(&hash, &salt); err != nil {
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
		_, err = tx.ExecContext(ctx, `
update users set password_hash = ?, password_salt = null, updated_at = ? where id = ?`,
			hashed, util.UtcNow(), sc.User.UserID)
		return err
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
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

const telegramAPIBase = "https://api.telegram.org"

func sendTelegramMessageFromEnv(ctx context.Context, chatID int64, text string) error {
	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if token == "" {
		return errors.New("telegram bot token is not configured")
	}
	values := url.Values{}
	values.Set("chat_id", fmt.Sprintf("%d", chatID))
	values.Set("text", text)
	values.Set("parse_mode", "HTML")
	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", telegramAPIBase, token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram sendMessage status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// RequireSameOriginUnsafe is route.SameOriginUnsafe, kept under the name the
// tests know.
func RequireSameOriginUnsafe(w http.ResponseWriter, r *http.Request) bool {
	return route.SameOriginUnsafe(w, r)
}

// createInvite is a small helper used by tests / future admin tooling. Not
// wired to an HTTP handler yet — invites are seeded out-of-band.
func createInvite(ctx context.Context, db *sql.DB, createdBy int64) (string, error) {
	now := time.Now().UTC()
	expires := now.Add(inviteLifetime).Format(time.RFC3339)
	for attempt := 0; attempt < 3; attempt++ {
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
