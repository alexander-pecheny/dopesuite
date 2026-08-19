package dopeserver

import (
	"context"
	"database/sql"
	"dope/dope/platform/util"
	"encoding/json"
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
)

const (
	trustedOriginHostsEnv = "DOPE_TRUSTED_ORIGIN_HOSTS"
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

func (s *server) handleAuthTgStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !RequireSameOriginUnsafe(w, r) {
		return
	}
	var res tglogin.StartResult
	err := s.inWriteTx(r.Context(), func(tx *sql.Tx) (err error) {
		res, err = s.handshake().Start(r.Context(), tx, time.Now())
		return err
	})
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSONValue(w, session.StartRegisterResponse{Code: res.Code, ExpiresAt: rfc3339(res.ExpiresAt), BotUsername: botUsername()})
}

// botUsername is the login bot's @handle, used to build the t.me deep link the
// login page offers. DOPE_BOT_NAME overrides the default.
func botUsername() string {
	if v := strings.TrimSpace(os.Getenv("DOPE_BOT_NAME")); v != "" {
		return v
	}
	return "dope_pecheny_bot"
}

func (s *server) handleAuthTgStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	var out tglogin.Outcome
	err := s.inWriteTx(r.Context(), func(tx *sql.Tx) (err error) {
		out, err = s.handshake().Resolve(r.Context(), tx, code, time.Now())
		return err
	})
	writeOutcome(w, out, err)
}

func (s *server) handleAuthTgClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !RequireSameOriginUnsafe(w, r) {
		return
	}
	defer r.Body.Close()
	var req struct {
		Code     string `json:"code"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(req.Username)
	if !util.ValidUsername(username) {
		writeAuthError(w, authError{code: http.StatusBadRequest, msg: "invalid username"})
		return
	}
	var out tglogin.Outcome
	err := s.inWriteTx(r.Context(), func(tx *sql.Tx) (err error) {
		out, err = s.handshake().Claim(r.Context(), tx, req.Code, username, req.Password, time.Now())
		switch {
		case errors.Is(err, tglogin.ErrCodeNotFound):
			return authError{code: http.StatusBadRequest, msg: "code not found"}
		case errors.Is(err, tglogin.ErrWrongPassword):
			return authError{code: http.StatusUnauthorized, msg: "wrong password"}
		case errors.Is(err, tglogin.ErrTelegramLinked):
			return authError{code: http.StatusConflict, msg: "telegram already linked"}
		}
		return err
	})
	writeOutcome(w, out, err)
}

func writeOutcome(w http.ResponseWriter, out tglogin.Outcome, err error) {
	if err != nil {
		writeAuthError(w, err)
		return
	}
	if out.Token != "" {
		session.SetCookie(w, out.Token)
	}
	writeJSONValue(w, session.RegisterStatusResponse{Status: out.Status, Username: out.Username})
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

func (s *server) handleAuthLoginPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !RequireSameOriginUnsafe(w, r) {
		return
	}
	defer r.Body.Close()
	var req loginPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(req.Username)
	password := req.Password
	if username == "" || password == "" {
		http.Error(w, "missing username or password", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	tx, err := s.eng.BeginWriteTx(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var (
		userID   int64
		hash     sql.NullString
		salt     sql.NullString
		isSystem int
	)
	err = tx.QueryRowContext(ctx, `
select id, password_hash, password_salt, is_system from users where username = ?`, username).Scan(
		&userID, &hash, &salt, &isSystem)
	if errors.Is(err, sql.ErrNoRows) || !hash.Valid {
		http.Error(w, "invalid username or password", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if isSystem == 1 {
		http.Error(w, "system user cannot log in", http.StatusForbidden)
		return
	}
	ok, upgraded, err := authcred.VerifyPasswordUpgrading(hash.String, salt.String, password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "invalid username or password", http.StatusUnauthorized)
		return
	}
	if upgraded != "" {
		// Lazy migration: upgrade legacy SHA256 hashes to bcrypt on first
		// successful login so the weaker hash leaves the database.
		if _, err := tx.ExecContext(ctx, `
update users set password_hash = ?, password_salt = null, updated_at = ? where id = ?`,
			upgraded, util.UtcNow(), userID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	now := time.Now().UTC()
	token, err := createSessionTx(ctx, tx, userID, now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	user, err := loadUserTx(ctx, tx, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	session.SetCookie(w, token)
	writeJSONValue(w, meResponseFor(user))
}

func (s *server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := s.eng.LookupSession(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSONValue(w, meResponseFor(user))
}

func (s *server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !RequireSameOriginUnsafe(w, r) {
		return
	}
	s.logoutSession(r)
	session.ClearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) logoutSession(r *http.Request) {
	if cookie, err := r.Cookie(session.CookieName); err == nil {
		hash := authcred.HashSessionToken(cookie.Value)
		_, _ = s.eng.DB.ExecContext(r.Context(), `delete from sessions where token_hash = ?`, hash)
	}
}

func (s *server) handleAuthUsername(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !RequireSameOriginUnsafe(w, r) {
		return
	}
	defer r.Body.Close()
	user, ok := s.eng.LookupSession(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Username.Valid {
		http.Error(w, "username already set", http.StatusConflict)
		return
	}
	var req usernameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(req.Username)
	if !util.ValidUsername(username) {
		http.Error(w, "bad username", http.StatusBadRequest)
		return
	}
	res, err := s.eng.WriteExec(r.Context(), `
update users set username = ?, updated_at = ? where id = ? and username is null`,
		username, util.UtcNow(), user.UserID)
	if err != nil {
		if util.IsUniqueViolation(err) {
			http.Error(w, "username taken", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		http.Error(w, "username already set", http.StatusConflict)
		return
	}
	user.Username = sql.NullString{String: username, Valid: true}
	writeJSONValue(w, meResponseFor(user))
}

// handleAuthPassword sets a password for the logged-in user, or changes an
// existing one. When a password is already set, the caller must supply the
// current password; the first time a password is set, no current password is
// required.
func (s *server) handleAuthPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !RequireSameOriginUnsafe(w, r) {
		return
	}
	defer r.Body.Close()
	user, ok := s.eng.LookupSession(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req passwordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if len(req.NewPassword) < passwordMinLen {
		http.Error(w, fmt.Sprintf("password must be at least %d characters", passwordMinLen), http.StatusBadRequest)
		return
	}
	if len(req.NewPassword) > passwordMaxLen {
		http.Error(w, fmt.Sprintf("password must be at most %d characters", passwordMaxLen), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	tx, err := s.eng.BeginWriteTx(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var (
		hash sql.NullString
		salt sql.NullString
	)
	if err := tx.QueryRowContext(ctx, `
select password_hash, password_salt from users where id = ?`, user.UserID).Scan(&hash, &salt); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Changing an existing password requires proving knowledge of the old one.
	if hash.Valid && hash.String != "" {
		ok, _, err := authcred.VerifyPasswordUpgrading(hash.String, salt.String, req.CurrentPassword)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "current password is incorrect", http.StatusUnauthorized)
			return
		}
	}
	hashed, err := authcred.HashPassword(req.NewPassword)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := tx.ExecContext(ctx, `
update users set password_hash = ?, password_salt = null, updated_at = ? where id = ?`,
		hashed, util.UtcNow(), user.UserID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

type authError struct {
	code int
	msg  string
}

func (e authError) Error() string { return e.msg }

func writeAuthError(w http.ResponseWriter, err error) {
	var ae authError
	if errors.As(err, &ae) {
		http.Error(w, ae.msg, ae.code)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
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

func RequireSameOriginUnsafe(w http.ResponseWriter, r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || !sameOriginRequestHost(u.Host, r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

func sameOriginRequestHost(originHost string, r *http.Request) bool {
	if strings.EqualFold(originHost, r.Host) {
		return true
	}
	if trustedOriginHost(originHost, os.Getenv(trustedOriginHostsEnv)) {
		return true
	}
	return false
}

func trustedOriginHost(originHost, trustedHosts string) bool {
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
