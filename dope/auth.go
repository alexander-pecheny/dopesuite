package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	sessionCookieName = "session"
	usernameMaxLen    = 32
	usernameMinLen    = 2
)

type startRegisterRequest struct {
	InviteCode string `json:"invite_code"`
}

type startRegisterResponse struct {
	Code      string `json:"code"`
	ExpiresAt string `json:"expires_at"`
}

type registerStatusResponse struct {
	Status   string  `json:"status"`
	Username *string `json:"username,omitempty"`
}

type loginRequest struct {
	Code string `json:"code"`
}

type usernameRequest struct {
	Username string `json:"username"`
}

type meResponse struct {
	UserID   int64   `json:"user_id"`
	Username *string `json:"username,omitempty"`
	Telegram *string `json:"telegram,omitempty"`
}

type sessionUser struct {
	SessionID int64
	UserID    int64
	Username  sql.NullString
	Telegram  sql.NullString
	IsSystem  bool
}

func (s *server) handleAuthRegisterStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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

func (s *server) startRegister(ctx context.Context, invite string) (startRegisterResponse, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return startRegisterResponse{}, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	var inviteID int64
	var usedBy sql.NullInt64
	var expiresAt string
	err = tx.QueryRowContext(ctx, `
select id, used_by, expires_at from invites where code = ?`, invite).Scan(&inviteID, &usedBy, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return startRegisterResponse{}, authError{code: http.StatusNotFound, msg: "invite not found"}
	}
	if err != nil {
		return startRegisterResponse{}, err
	}
	if usedBy.Valid {
		return startRegisterResponse{}, authError{code: http.StatusGone, msg: "invite already used"}
	}
	expiry, err := time.Parse(time.RFC3339, expiresAt)
	if err == nil && now.After(expiry) {
		return startRegisterResponse{}, authError{code: http.StatusGone, msg: "invite expired"}
	}

	expires := now.Add(telegramAuthLifetime)
	for attempt := 0; attempt < 3; attempt++ {
		code, err := newTelegramAuthCode()
		if err != nil {
			return startRegisterResponse{}, err
		}
		_, err = tx.ExecContext(ctx, `
insert into telegram_login_codes(code, kind, invite_id, created_at, expires_at)
values(?, 'register', ?, ?, ?)`, code, inviteID, now.Format(time.RFC3339), expires.Format(time.RFC3339))
		if err == nil {
			if err := tx.Commit(); err != nil {
				return startRegisterResponse{}, err
			}
			return startRegisterResponse{Code: code, ExpiresAt: expires.Format(time.RFC3339)}, nil
		}
		if !isUniqueViolation(err) {
			return startRegisterResponse{}, err
		}
	}
	return startRegisterResponse{}, errors.New("could not allocate register code")
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
		setSessionCookie(w, token)
	}
	writeJSONValue(w, resp)
}

func (s *server) finalizeRegister(ctx context.Context, code string) (registerStatusResponse, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return registerStatusResponse{}, "", err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	var (
		codeID         int64
		kind           string
		inviteID       sql.NullInt64
		userID         sql.NullInt64
		tgUserID       sql.NullInt64
		tgUsername     sql.NullString
		expiresAt      string
		consumedAt     sql.NullString
	)
	err = tx.QueryRowContext(ctx, `
select id, kind, invite_id, user_id, telegram_user_id, telegram_username, expires_at, consumed_at
from telegram_login_codes where code = ?`, code).Scan(
		&codeID, &kind, &inviteID, &userID, &tgUserID, &tgUsername, &expiresAt, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return registerStatusResponse{Status: "not_found"}, "", nil
	}
	if err != nil {
		return registerStatusResponse{}, "", err
	}
	if kind != "register" {
		return registerStatusResponse{Status: "not_found"}, "", nil
	}
	expiry, _ := time.Parse(time.RFC3339, expiresAt)
	if !consumedAt.Valid {
		if !expiry.IsZero() && now.After(expiry) {
			return registerStatusResponse{Status: "expired"}, "", nil
		}
		return registerStatusResponse{Status: "pending"}, "", nil
	}
	if !tgUserID.Valid {
		return registerStatusResponse{Status: "pending"}, "", nil
	}
	if !inviteID.Valid {
		return registerStatusResponse{}, "", errors.New("register code missing invite")
	}

	var inviteUsedBy sql.NullInt64
	var inviteExpiresAt string
	if err := tx.QueryRowContext(ctx, `
select used_by, expires_at from invites where id = ?`, inviteID.Int64).Scan(&inviteUsedBy, &inviteExpiresAt); err != nil {
		return registerStatusResponse{}, "", err
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
				return registerStatusResponse{}, "", err
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			id, err := insertReturningID(ctx, tx, `
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(?, ?, null, 0, ?, ?)`, tgUserID.Int64, tgUsername, now.Format(time.RFC3339), now.Format(time.RFC3339))
			if err != nil {
				return registerStatusResponse{}, "", err
			}
			resolvedUserID = id
		} else {
			return registerStatusResponse{}, "", err
		}

		if _, err := tx.ExecContext(ctx, `
update telegram_login_codes set user_id = ? where id = ?`, resolvedUserID, codeID); err != nil {
			return registerStatusResponse{}, "", err
		}

		if !inviteUsedBy.Valid {
			if _, err := tx.ExecContext(ctx, `
update invites set used_by = ?, used_at = ? where id = ? and used_by is null`,
				resolvedUserID, now.Format(time.RFC3339), inviteID.Int64); err != nil {
				return registerStatusResponse{}, "", err
			}
		}
	}

	token, err := createSessionTx(ctx, tx, resolvedUserID, now)
	if err != nil {
		return registerStatusResponse{}, "", err
	}

	var username sql.NullString
	if err := tx.QueryRowContext(ctx, `select username from users where id = ?`, resolvedUserID).Scan(&username); err != nil {
		return registerStatusResponse{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return registerStatusResponse{}, "", err
	}
	resp := registerStatusResponse{Status: "ready"}
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
	setSessionCookie(w, token)
	writeJSONValue(w, meResponseFor(user))
}

func (s *server) consumeLoginCode(ctx context.Context, code string) (string, sessionUser, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", sessionUser{}, err
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
		return "", sessionUser{}, authError{code: http.StatusNotFound, msg: "code not found"}
	}
	if err != nil {
		return "", sessionUser{}, err
	}
	if kind != "login" {
		return "", sessionUser{}, authError{code: http.StatusBadRequest, msg: "wrong code kind"}
	}
	if consumedAt.Valid {
		return "", sessionUser{}, authError{code: http.StatusGone, msg: "code already used"}
	}
	expiry, _ := time.Parse(time.RFC3339, expiresAt)
	if !expiry.IsZero() && now.After(expiry) {
		return "", sessionUser{}, authError{code: http.StatusGone, msg: "code expired"}
	}
	if !userID.Valid {
		return "", sessionUser{}, errors.New("login code missing user")
	}

	if _, err := tx.ExecContext(ctx, `
update telegram_login_codes set consumed_at = ? where id = ?`, now.Format(time.RFC3339), codeID); err != nil {
		return "", sessionUser{}, err
	}

	user, err := loadUserTx(ctx, tx, userID.Int64)
	if err != nil {
		return "", sessionUser{}, err
	}
	if user.IsSystem {
		return "", sessionUser{}, authError{code: http.StatusForbidden, msg: "system user cannot log in"}
	}
	token, err := createSessionTx(ctx, tx, user.UserID, now)
	if err != nil {
		return "", sessionUser{}, err
	}
	if err := tx.Commit(); err != nil {
		return "", sessionUser{}, err
	}
	return token, user, nil
}

func (s *server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := s.lookupSession(r)
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
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		hash := hashSessionToken(cookie.Value)
		_, _ = s.db.ExecContext(r.Context(), `delete from sessions where token_hash = ?`, hash)
	}
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleAuthUsername(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	user, ok := s.lookupSession(r)
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
	if !validUsername(username) {
		http.Error(w, "bad username", http.StatusBadRequest)
		return
	}
	res, err := s.db.ExecContext(r.Context(), `
update users set username = ?, updated_at = ? where id = ? and username is null`,
		username, utcNow(), user.UserID)
	if err != nil {
		if isUniqueViolation(err) {
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

func (s *server) lookupSession(r *http.Request) (sessionUser, bool) {
	if s.db == nil {
		return sessionUser{}, false
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return sessionUser{}, false
	}
	hash := hashSessionToken(cookie.Value)

	ctx := r.Context()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return sessionUser{}, false
	}
	defer tx.Rollback()

	var (
		sessionID int64
		userID    int64
		expiresAt string
	)
	err = tx.QueryRowContext(ctx, `
select id, user_id, expires_at from sessions where token_hash = ?`, hash).Scan(&sessionID, &userID, &expiresAt)
	if err != nil {
		return sessionUser{}, false
	}
	now := time.Now().UTC()
	expiry, _ := time.Parse(time.RFC3339, expiresAt)
	if !expiry.IsZero() && now.After(expiry) {
		_, _ = tx.ExecContext(ctx, `delete from sessions where id = ?`, sessionID)
		_ = tx.Commit()
		return sessionUser{}, false
	}
	newExpires := now.Add(sessionLifetime).Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
update sessions set last_seen_at = ?, expires_at = ? where id = ?`,
		now.Format(time.RFC3339), newExpires, sessionID); err != nil {
		return sessionUser{}, false
	}

	user, err := loadUserTx(ctx, tx, userID)
	if err != nil {
		return sessionUser{}, false
	}
	user.SessionID = sessionID
	if err := tx.Commit(); err != nil {
		return sessionUser{}, false
	}
	return user, true
}

func loadUserTx(ctx context.Context, tx *sql.Tx, userID int64) (sessionUser, error) {
	var (
		username sql.NullString
		tgUser   sql.NullString
		isSystem int
	)
	err := tx.QueryRowContext(ctx, `
select username, telegram_username, is_system from users where id = ?`, userID).Scan(&username, &tgUser, &isSystem)
	if err != nil {
		return sessionUser{}, err
	}
	return sessionUser{
		UserID:   userID,
		Username: username,
		Telegram: tgUser,
		IsSystem: isSystem == 1,
	}, nil
}

func createSessionTx(ctx context.Context, tx *sql.Tx, userID int64, now time.Time) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		token, err := newSessionToken()
		if err != nil {
			return "", err
		}
		hash := hashSessionToken(token)
		expires := now.Add(sessionLifetime).Format(time.RFC3339)
		nowStr := now.Format(time.RFC3339)
		_, err = tx.ExecContext(ctx, `
insert into sessions(user_id, token_hash, created_at, expires_at, last_seen_at)
values(?, ?, ?, ?, ?)`, userID, hash, nowStr, expires, nowStr)
		if err == nil {
			return token, nil
		}
		if !isUniqueViolation(err) {
			return "", err
		}
	}
	return "", errors.New("could not allocate session token")
}

func meResponseFor(user sessionUser) meResponse {
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

func setSessionCookie(w http.ResponseWriter, token string) {
	cookie := &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isProdEnv(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionLifetime / time.Second),
	}
	http.SetCookie(w, cookie)
}

func clearSessionCookie(w http.ResponseWriter) {
	cookie := &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isProdEnv(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
	http.SetCookie(w, cookie)
}

func isProdEnv() bool {
	return strings.EqualFold(os.Getenv("DOPE_ENV"), "production")
}

func validUsername(s string) bool {
	if len(s) < usernameMinLen || len(s) > usernameMaxLen {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		default:
			return false
		}
	}
	return true
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "constraint")
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

// createInvite is a small helper used by tests / future admin tooling. Not
// wired to an HTTP handler yet — invites are seeded out-of-band.
func createInvite(ctx context.Context, db *sql.DB, createdBy int64) (string, error) {
	now := time.Now().UTC()
	expires := now.Add(inviteLifetime).Format(time.RFC3339)
	for attempt := 0; attempt < 3; attempt++ {
		code, err := newInviteCode()
		if err != nil {
			return "", err
		}
		_, err = db.ExecContext(ctx, `
insert into invites(code, created_by, created_at, expires_at)
values(?, ?, ?, ?)`, code, createdBy, now.Format(time.RFC3339), expires)
		if err == nil {
			return code, nil
		}
		if !isUniqueViolation(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("could not allocate invite code")
}
