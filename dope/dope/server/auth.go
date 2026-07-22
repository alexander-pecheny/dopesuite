package dopeserver

import (
	"context"
	"database/sql"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
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

func (s *server) handleAuthTgStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !RequireSameOriginUnsafe(w, r) {
		return
	}
	resp, err := s.tgStart(r.Context())
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSONValue(w, resp)
}

// botUsername is the login bot's @handle, used to build the t.me deep link the
// login page offers. DOPE_BOT_NAME overrides the default.
func botUsername() string {
	if v := strings.TrimSpace(os.Getenv("DOPE_BOT_NAME")); v != "" {
		return v
	}
	return "dope_pecheny_bot"
}

// tgStart mints a bot code for the telegram handshake — no username up front. Who
// the visitor is (and, for a new account, the username) is settled afterwards by
// tgStatus / tgClaim.
func (s *server) tgStart(ctx context.Context) (session.StartRegisterResponse, error) {
	tx, err := s.eng.BeginWriteTx(ctx)
	if err != nil {
		return session.StartRegisterResponse{}, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	// Opportunistically reap lapsed codes so consumed-but-abandoned rows don't
	// linger as replay fodder.
	if _, err := tx.ExecContext(ctx, `delete from telegram_login_codes where expires_at < ?`, now.Format(time.RFC3339)); err != nil {
		return session.StartRegisterResponse{}, err
	}
	expires := now.Add(session.TelegramAuthLifetime)
	for attempt := 0; attempt < 3; attempt++ {
		code, err := authcred.NewTelegramAuthCode()
		if err != nil {
			return session.StartRegisterResponse{}, err
		}
		_, err = tx.ExecContext(ctx, `
insert into telegram_login_codes(code, kind, created_at, expires_at)
values(?, 'register', ?, ?)`, code, now.Format(time.RFC3339), expires.Format(time.RFC3339))
		if err == nil {
			if err := tx.Commit(); err != nil {
				return session.StartRegisterResponse{}, err
			}
			return session.StartRegisterResponse{Code: code, ExpiresAt: expires.Format(time.RFC3339), BotUsername: botUsername()}, nil
		}
		if !util.IsUniqueViolation(err) {
			return session.StartRegisterResponse{}, err
		}
	}
	return session.StartRegisterResponse{}, errors.New("could not allocate code")
}

func (s *server) handleAuthTgStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	code := strings.TrimSpace(strings.ToUpper(r.URL.Query().Get("code")))
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	resp, token, err := s.tgStatus(r.Context(), code)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	if token != "" {
		session.SetCookie(w, token)
	}
	writeJSONValue(w, resp)
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
	resp, token, err := s.tgClaim(r.Context(), strings.TrimSpace(strings.ToUpper(req.Code)), strings.TrimSpace(req.Username), req.Password)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	if token != "" {
		session.SetCookie(w, token)
	}
	writeJSONValue(w, resp)
}

// tgStatus resolves a confirmed handshake code. A known telegram logs straight in
// (ready); a brand-new telegram returns choose_username, and its account is
// created later by tgClaim. Statuses: ready, choose_username, pending, expired,
// not_found.
func (s *server) tgStatus(ctx context.Context, code string) (session.RegisterStatusResponse, string, error) {
	tx, err := s.eng.BeginWriteTx(ctx)
	if err != nil {
		return session.RegisterStatusResponse{}, "", err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	var (
		kind       string
		tgUserID   sql.NullInt64
		tgUsername sql.NullString
		expiresAt  string
		consumedAt sql.NullString
	)
	err = tx.QueryRowContext(ctx, `
select kind, telegram_user_id, telegram_username, expires_at, consumed_at
from telegram_login_codes where code = ?`, code).Scan(&kind, &tgUserID, &tgUsername, &expiresAt, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && kind != "register") {
		return session.RegisterStatusResponse{Status: "not_found"}, "", nil
	}
	if err != nil {
		return session.RegisterStatusResponse{}, "", err
	}
	// Expiry bounds the whole handshake, consumed or not — a code leaked via the
	// status URL can't be replayed into a session once it lapses.
	expiry, _ := time.Parse(time.RFC3339, expiresAt)
	if !expiry.IsZero() && now.After(expiry) {
		return session.RegisterStatusResponse{Status: "expired"}, "", nil
	}
	if !consumedAt.Valid || !tgUserID.Valid {
		return session.RegisterStatusResponse{Status: "pending"}, "", nil
	}

	var uid int64
	var uname sql.NullString
	switch err := tx.QueryRowContext(ctx, `select id, username from users where telegram_user_id = ? and is_system = 0`, tgUserID.Int64).Scan(&uid, &uname); {
	case err == nil:
		if _, err := tx.ExecContext(ctx, `update users set telegram_username = ?, updated_at = ? where id = ?`,
			tgUsername, now.Format(time.RFC3339), uid); err != nil {
			return session.RegisterStatusResponse{}, "", err
		}
		token, err := createSessionTx(ctx, tx, uid, now)
		if err != nil {
			return session.RegisterStatusResponse{}, "", err
		}
		// Burn the code so it can't be replayed for another session.
		if _, err := tx.ExecContext(ctx, `delete from telegram_login_codes where code = ?`, code); err != nil {
			return session.RegisterStatusResponse{}, "", err
		}
		if err := tx.Commit(); err != nil {
			return session.RegisterStatusResponse{}, "", err
		}
		resp := session.RegisterStatusResponse{Status: "ready"}
		if uname.Valid {
			v := uname.String
			resp.Username = &v
		}
		return resp, token, nil
	case errors.Is(err, sql.ErrNoRows):
		return session.RegisterStatusResponse{Status: "choose_username"}, "", nil
	default:
		return session.RegisterStatusResponse{}, "", err
	}
}

// tgClaim finishes a brand-new telegram account: the visitor picks a username.
// Free → create + log in. An existing password account → link once the password
// is proven (password_required until then). Taken by another telegram →
// username_taken.
func (s *server) tgClaim(ctx context.Context, code, username, password string) (session.RegisterStatusResponse, string, error) {
	if !util.ValidUsername(username) {
		return session.RegisterStatusResponse{}, "", authError{code: http.StatusBadRequest, msg: "invalid username"}
	}
	tx, err := s.eng.BeginWriteTx(ctx)
	if err != nil {
		return session.RegisterStatusResponse{}, "", err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	var (
		tgUserID   sql.NullInt64
		tgUsername sql.NullString
		tgName     sql.NullString
	)
	err = tx.QueryRowContext(ctx, `
select telegram_user_id, telegram_username, telegram_name
from telegram_login_codes
where code = ? and kind = 'register' and consumed_at is not null and expires_at > ?`, code, now.Format(time.RFC3339)).Scan(
		&tgUserID, &tgUsername, &tgName)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !tgUserID.Valid) {
		return session.RegisterStatusResponse{}, "", authError{code: http.StatusBadRequest, msg: "code not found"}
	}
	if err != nil {
		return session.RegisterStatusResponse{}, "", err
	}
	// delCode burns the code on any successful login/link so it can't be replayed.
	delCode := func() error {
		_, e := tx.ExecContext(ctx, `delete from telegram_login_codes where code = ?`, code)
		return e
	}

	// This telegram may already resolve to an account (double-submit / race).
	var euid int64
	var euname sql.NullString
	switch err := tx.QueryRowContext(ctx, `select id, username from users where telegram_user_id = ? and is_system = 0`, tgUserID.Int64).Scan(&euid, &euname); {
	case err == nil:
		token, err := createSessionTx(ctx, tx, euid, now)
		if err != nil {
			return session.RegisterStatusResponse{}, "", err
		}
		if err := delCode(); err != nil {
			return session.RegisterStatusResponse{}, "", err
		}
		if err := tx.Commit(); err != nil {
			return session.RegisterStatusResponse{}, "", err
		}
		v := username
		if euname.Valid {
			v = euname.String
		}
		return session.RegisterStatusResponse{Status: "ready", Username: &v}, token, nil
	case !errors.Is(err, sql.ErrNoRows):
		return session.RegisterStatusResponse{}, "", err
	}

	var uid int64
	var hash, salt sql.NullString
	switch err := tx.QueryRowContext(ctx, `select id, password_hash, password_salt from users where username = ? and is_system = 0`, username).Scan(&uid, &hash, &salt); {
	case errors.Is(err, sql.ErrNoRows):
		nid, ierr := store.InsertReturningID(ctx, tx, `
insert into users(telegram_user_id, telegram_username, telegram_name, username, is_system, created_at, updated_at)
values(?, ?, ?, ?, 0, ?, ?)`, tgUserID.Int64, tgUsername, tgName, username, now.Format(time.RFC3339), now.Format(time.RFC3339))
		if util.IsUniqueViolation(ierr) {
			return session.RegisterStatusResponse{Status: "username_taken"}, "", nil
		}
		if ierr != nil {
			return session.RegisterStatusResponse{}, "", ierr
		}
		token, terr := createSessionTx(ctx, tx, nid, now)
		if terr != nil {
			return session.RegisterStatusResponse{}, "", terr
		}
		if err := delCode(); err != nil {
			return session.RegisterStatusResponse{}, "", err
		}
		if err := tx.Commit(); err != nil {
			return session.RegisterStatusResponse{}, "", err
		}
		v := username
		return session.RegisterStatusResponse{Status: "ready", Username: &v}, token, nil
	case err != nil:
		return session.RegisterStatusResponse{}, "", err
	case hash.Valid && hash.String != "":
		// Existing password account: link only once the password is proven.
		if password == "" {
			return session.RegisterStatusResponse{Status: "password_required"}, "", nil
		}
		ok, _, verr := authcred.VerifyPasswordUpgrading(hash.String, salt.String, password)
		if verr != nil {
			return session.RegisterStatusResponse{}, "", verr
		}
		if !ok {
			return session.RegisterStatusResponse{}, "", authError{code: http.StatusUnauthorized, msg: "wrong password"}
		}
		if _, err := tx.ExecContext(ctx, `
update users set telegram_user_id = ?, telegram_username = ?, telegram_name = ?, updated_at = ? where id = ?`,
			tgUserID.Int64, tgUsername, tgName, now.Format(time.RFC3339), uid); err != nil {
			if util.IsUniqueViolation(err) {
				return session.RegisterStatusResponse{}, "", authError{code: http.StatusConflict, msg: "telegram already linked"}
			}
			return session.RegisterStatusResponse{}, "", err
		}
		token, terr := createSessionTx(ctx, tx, uid, now)
		if terr != nil {
			return session.RegisterStatusResponse{}, "", terr
		}
		if err := delCode(); err != nil {
			return session.RegisterStatusResponse{}, "", err
		}
		if err := tx.Commit(); err != nil {
			return session.RegisterStatusResponse{}, "", err
		}
		v := username
		return session.RegisterStatusResponse{Status: "ready", Username: &v}, token, nil
	default:
		return session.RegisterStatusResponse{Status: "username_taken"}, "", nil
	}
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
