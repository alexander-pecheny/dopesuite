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

type startRegisterRequest struct {
	InviteCode string `json:"invite_code"`
}

type loginRequest struct {
	Code string `json:"code"`
}

type loginStartRequest struct {
	Username string `json:"username"`
	SendCode bool   `json:"send_code,omitempty"`
}

type loginStartResponse struct {
	Username    string `json:"username"`
	HasPassword bool   `json:"has_password"`
	CodeSent    bool   `json:"code_sent"`
	ExpiresAt   string `json:"expires_at,omitempty"`
}

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

func (s *server) handleAuthRegisterStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !RequireSameOriginUnsafe(w, r) {
		return
	}
	defer r.Body.Close()

	var req startRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	invite := strings.TrimSpace(strings.ToUpper(req.InviteCode))
	if invite == "" {
		http.Error(w, "missing invite code", http.StatusBadRequest)
		return
	}

	resp, err := s.startRegister(r.Context(), invite)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSONValue(w, resp)
}

func (s *server) startRegister(ctx context.Context, invite string) (session.StartRegisterResponse, error) {
	tx, err := s.eng.BeginWriteTx(ctx)
	if err != nil {
		return session.StartRegisterResponse{}, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	var inviteID int64
	var usedBy sql.NullInt64
	var expiresAt string
	err = tx.QueryRowContext(ctx, `
select id, used_by, expires_at from invites where code = ?`, invite).Scan(&inviteID, &usedBy, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return session.StartRegisterResponse{}, authError{code: http.StatusNotFound, msg: "invite not found"}
	}
	if err != nil {
		return session.StartRegisterResponse{}, err
	}
	if usedBy.Valid {
		return session.StartRegisterResponse{}, authError{code: http.StatusGone, msg: "invite already used"}
	}
	expiry, err := time.Parse(time.RFC3339, expiresAt)
	if err == nil && now.After(expiry) {
		return session.StartRegisterResponse{}, authError{code: http.StatusGone, msg: "invite expired"}
	}

	expires := now.Add(session.TelegramAuthLifetime)
	for attempt := 0; attempt < 3; attempt++ {
		code, err := authcred.NewTelegramAuthCode()
		if err != nil {
			return session.StartRegisterResponse{}, err
		}
		_, err = tx.ExecContext(ctx, `
insert into telegram_login_codes(code, kind, invite_id, created_at, expires_at)
values(?, 'register', ?, ?, ?)`, code, inviteID, now.Format(time.RFC3339), expires.Format(time.RFC3339))
		if err == nil {
			if err := tx.Commit(); err != nil {
				return session.StartRegisterResponse{}, err
			}
			return session.StartRegisterResponse{Code: code, ExpiresAt: expires.Format(time.RFC3339)}, nil
		}
		if !util.IsUniqueViolation(err) {
			return session.StartRegisterResponse{}, err
		}
	}
	return session.StartRegisterResponse{}, errors.New("could not allocate register code")
}

func (s *server) handleAuthRegisterStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	code := strings.TrimSpace(strings.ToUpper(r.URL.Query().Get("code")))
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	resp, token, err := s.finalizeRegister(r.Context(), code)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	if token != "" {
		session.SetCookie(w, token)
	}
	writeJSONValue(w, resp)
}

func (s *server) finalizeRegister(ctx context.Context, code string) (session.RegisterStatusResponse, string, error) {
	tx, err := s.eng.BeginWriteTx(ctx)
	if err != nil {
		return session.RegisterStatusResponse{}, "", err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	var (
		codeID     int64
		kind       string
		inviteID   sql.NullInt64
		userID     sql.NullInt64
		tgUserID   sql.NullInt64
		tgUsername sql.NullString
		expiresAt  string
		consumedAt sql.NullString
	)
	err = tx.QueryRowContext(ctx, `
select id, kind, invite_id, user_id, telegram_user_id, telegram_username, expires_at, consumed_at
from telegram_login_codes where code = ?`, code).Scan(
		&codeID, &kind, &inviteID, &userID, &tgUserID, &tgUsername, &expiresAt, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return session.RegisterStatusResponse{Status: "not_found"}, "", nil
	}
	if err != nil {
		return session.RegisterStatusResponse{}, "", err
	}
	if kind != "register" {
		return session.RegisterStatusResponse{Status: "not_found"}, "", nil
	}
	expiry, _ := time.Parse(time.RFC3339, expiresAt)
	if !consumedAt.Valid {
		if !expiry.IsZero() && now.After(expiry) {
			return session.RegisterStatusResponse{Status: "expired"}, "", nil
		}
		return session.RegisterStatusResponse{Status: "pending"}, "", nil
	}
	if !tgUserID.Valid {
		return session.RegisterStatusResponse{Status: "pending"}, "", nil
	}
	if !inviteID.Valid {
		return session.RegisterStatusResponse{}, "", errors.New("register code missing invite")
	}

	var inviteUsedBy sql.NullInt64
	var inviteExpiresAt string
	if err := tx.QueryRowContext(ctx, `
select used_by, expires_at from invites where id = ?`, inviteID.Int64).Scan(&inviteUsedBy, &inviteExpiresAt); err != nil {
		return session.RegisterStatusResponse{}, "", err
	}

	var resolvedUserID int64
	if userID.Valid {
		resolvedUserID = userID.Int64
	} else {
		var existing int64
		err := tx.QueryRowContext(ctx, `select id from users where telegram_user_id = ?`, tgUserID.Int64).Scan(&existing)
		if err == nil {
			resolvedUserID = existing
			if _, err := tx.ExecContext(ctx, `
update users set telegram_username = ?, updated_at = ? where id = ?`, tgUsername, now.Format(time.RFC3339), existing); err != nil {
				return session.RegisterStatusResponse{}, "", err
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			id, err := store.InsertReturningID(ctx, tx, `
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(?, ?, null, 0, ?, ?)`, tgUserID.Int64, tgUsername, now.Format(time.RFC3339), now.Format(time.RFC3339))
			if err != nil {
				return session.RegisterStatusResponse{}, "", err
			}
			resolvedUserID = id
		} else {
			return session.RegisterStatusResponse{}, "", err
		}

		if _, err := tx.ExecContext(ctx, `
update telegram_login_codes set user_id = ? where id = ?`, resolvedUserID, codeID); err != nil {
			return session.RegisterStatusResponse{}, "", err
		}

		if !inviteUsedBy.Valid {
			if _, err := tx.ExecContext(ctx, `
update invites set used_by = ?, used_at = ? where id = ? and used_by is null`,
				resolvedUserID, now.Format(time.RFC3339), inviteID.Int64); err != nil {
				return session.RegisterStatusResponse{}, "", err
			}
		}
	}

	token, err := createSessionTx(ctx, tx, resolvedUserID, now)
	if err != nil {
		return session.RegisterStatusResponse{}, "", err
	}

	var username sql.NullString
	if err := tx.QueryRowContext(ctx, `select username from users where id = ?`, resolvedUserID).Scan(&username); err != nil {
		return session.RegisterStatusResponse{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return session.RegisterStatusResponse{}, "", err
	}
	resp := session.RegisterStatusResponse{Status: "ready"}
	if username.Valid {
		v := username.String
		resp.Username = &v
	}
	return resp, token, nil
}

func (s *server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !RequireSameOriginUnsafe(w, r) {
		return
	}
	defer r.Body.Close()
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	code := strings.TrimSpace(strings.ToUpper(req.Code))
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	token, user, err := s.consumeLoginCode(r.Context(), code)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	session.SetCookie(w, token)
	writeJSONValue(w, meResponseFor(user))
}

func (s *server) handleAuthLoginStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !RequireSameOriginUnsafe(w, r) {
		return
	}
	defer r.Body.Close()
	var req loginStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		http.Error(w, "missing username", http.StatusBadRequest)
		return
	}

	resp, err := s.startLogin(r.Context(), username, req.SendCode)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSONValue(w, resp)
}

func (s *server) startLogin(ctx context.Context, username string, sendCode bool) (loginStartResponse, error) {
	var (
		userID     int64
		tgUserID   sql.NullInt64
		tgUsername sql.NullString
		hash       sql.NullString
		salt       sql.NullString
		isSystem   int
	)
	err := s.eng.DB.QueryRowContext(ctx, `
select id, telegram_user_id, telegram_username, password_hash, password_salt, is_system
from users where username = ?`, username).Scan(&userID, &tgUserID, &tgUsername, &hash, &salt, &isSystem)
	if errors.Is(err, sql.ErrNoRows) {
		return loginStartResponse{}, authError{code: http.StatusNotFound, msg: "user not found"}
	}
	if err != nil {
		return loginStartResponse{}, err
	}
	if isSystem == 1 {
		return loginStartResponse{}, authError{code: http.StatusForbidden, msg: "system user cannot log in"}
	}

	// bcrypt hashes carry their own salt, so a non-null hash is enough.
	// Legacy SHA256 entries still have salt.Valid; both are accepted at
	// verify time by verifyPassword.
	hasPassword := hash.Valid && strings.TrimSpace(hash.String) != ""
	_ = salt
	resp := loginStartResponse{Username: username, HasPassword: hasPassword}
	if hasPassword && !sendCode {
		return resp, nil
	}
	if !tgUserID.Valid {
		return loginStartResponse{}, authError{code: http.StatusConflict, msg: "telegram account is not linked"}
	}

	code, expiresAt, err := s.issueLoginCode(ctx, userID, tgUserID.Int64, tgUsername)
	if err != nil {
		return loginStartResponse{}, err
	}
	if err := s.sendLoginCode(ctx, tgUserID.Int64, code); err != nil {
		_, _ = s.eng.DB.ExecContext(ctx, `
delete from telegram_login_codes where code = ? and consumed_at is null`, code)
		return loginStartResponse{}, authError{code: http.StatusServiceUnavailable, msg: "could not send telegram code"}
	}
	resp.CodeSent = true
	resp.ExpiresAt = expiresAt
	return resp, nil
}

func (s *server) issueLoginCode(ctx context.Context, userID int64, tgUserID int64, tgUsername sql.NullString) (string, string, error) {
	now := time.Now().UTC()
	expires := now.Add(session.TelegramAuthLifetime).Format(time.RFC3339)
	createdAt := now.Format(time.RFC3339)
	for attempt := 0; attempt < 3; attempt++ {
		code, err := authcred.NewTelegramLoginCode()
		if err != nil {
			return "", "", err
		}
		_, err = s.eng.DB.ExecContext(ctx, `
insert into telegram_login_codes(code, kind, user_id, telegram_user_id, telegram_username, created_at, expires_at)
values(?, 'login', ?, ?, ?, ?, ?)`, code, userID, tgUserID, tgUsername, createdAt, expires)
		if err == nil {
			return code, expires, nil
		}
		if !util.IsUniqueViolation(err) {
			return "", "", err
		}
	}
	return "", "", errors.New("could not allocate login code")
}

func (s *server) sendLoginCode(ctx context.Context, chatID int64, code string) error {
	sender := s.SendTelegram
	if sender == nil {
		sender = sendTelegramMessageFromEnv
	}
	return sender(ctx, chatID, loginCodeTelegramMessage(code))
}

func loginCodeTelegramMessage(code string) string {
	return "Твой код для входа:\n<code>" + code + "</code>\nВведи его на странице входа в течение минуты."
}

func (s *server) consumeLoginCode(ctx context.Context, code string) (string, session.User, error) {
	tx, err := s.eng.BeginWriteTx(ctx)
	if err != nil {
		return "", session.User{}, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	var (
		codeID     int64
		kind       string
		userID     sql.NullInt64
		expiresAt  string
		consumedAt sql.NullString
	)
	err = tx.QueryRowContext(ctx, `
select id, kind, user_id, expires_at, consumed_at from telegram_login_codes where code = ?`, code).Scan(
		&codeID, &kind, &userID, &expiresAt, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", session.User{}, authError{code: http.StatusNotFound, msg: "code not found"}
	}
	if err != nil {
		return "", session.User{}, err
	}
	if kind != "login" {
		return "", session.User{}, authError{code: http.StatusBadRequest, msg: "wrong code kind"}
	}
	if consumedAt.Valid {
		return "", session.User{}, authError{code: http.StatusGone, msg: "code already used"}
	}
	expiry, _ := time.Parse(time.RFC3339, expiresAt)
	if !expiry.IsZero() && now.After(expiry) {
		return "", session.User{}, authError{code: http.StatusGone, msg: "code expired"}
	}
	if !userID.Valid {
		return "", session.User{}, errors.New("login code missing user")
	}

	if _, err := tx.ExecContext(ctx, `
update telegram_login_codes set consumed_at = ? where id = ?`, now.Format(time.RFC3339), codeID); err != nil {
		return "", session.User{}, err
	}

	user, err := loadUserTx(ctx, tx, userID.Int64)
	if err != nil {
		return "", session.User{}, err
	}
	if user.IsSystem {
		return "", session.User{}, authError{code: http.StatusForbidden, msg: "system user cannot log in"}
	}
	token, err := createSessionTx(ctx, tx, user.UserID, now)
	if err != nil {
		return "", session.User{}, err
	}
	if err := tx.Commit(); err != nil {
		return "", session.User{}, err
	}
	return token, user, nil
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
