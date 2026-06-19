package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"xy/internal/session"
)

const (
	sessionTokenBytes        = 32
	inviteCodeBytes          = 12
	telegramAuthBytes        = 12
	telegramLoginCodeLen     = 8
	telegramLoginCodeAlpha   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	passwordMinLen           = 8
	passwordMaxLen           = 72 // bcrypt limit
	sessionRefreshGranlarity = time.Minute
)

var base32enc = base32.StdEncoding.WithPadding(base32.NoPadding)

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// ---- credential generation ----

func randomBase32(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base32enc.EncodeToString(buf), nil
}

func newSessionToken() (string, error) {
	buf := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func newInviteCode() (string, error)       { return randomBase32(inviteCodeBytes) }
func newTelegramAuthCode() (string, error) { return randomBase32(telegramAuthBytes) }

func newTelegramLoginCode() (string, error) {
	buf := make([]byte, telegramLoginCodeLen)
	max := big.NewInt(int64(len(telegramLoginCodeAlpha)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = telegramLoginCodeAlpha[n.Int64()]
	}
	return string(buf), nil
}

func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ---- password hashing ----

func hashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(h), err
}

func verifyPassword(storedHash, pw string) bool {
	if storedHash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(pw)) == nil
}

// ---- session lookup / middleware ----

// lookupSession resolves the session cookie to a user, refreshing the sliding
// expiry at most once a minute. Returns ok=false if no valid session.
func (s *server) lookupSession(w http.ResponseWriter, r *http.Request) (session.User, bool) {
	c, err := r.Cookie(session.CookieName)
	if err != nil || c.Value == "" {
		return session.User{}, false
	}
	hash := hashSessionToken(c.Value)
	var (
		u           session.User
		expiresStr  string
		lastSeenStr string
	)
	row := s.db.QueryRowContext(r.Context(), `
select s.id, s.user_id, s.expires_at, s.last_seen_at, u.username, u.telegram_username
from sessions s join users u on u.id = s.user_id
where s.token_hash = ?`, hash)
	if err := row.Scan(&u.SessionID, &u.UserID, &expiresStr, &lastSeenStr, &u.Username, &u.Telegram); err != nil {
		return session.User{}, false
	}
	now := time.Now()
	expires, _ := time.Parse(time.RFC3339, expiresStr)
	if now.After(expires) {
		_ = s.withWriteTx(r.Context(), "session-expire", func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `delete from sessions where id = ?`, u.SessionID)
			return err
		})
		return session.User{}, false
	}
	if lastSeen, _ := time.Parse(time.RFC3339, lastSeenStr); now.Sub(lastSeen) >= sessionRefreshGranlarity {
		_ = s.withWriteTx(r.Context(), "session-refresh", func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `update sessions set last_seen_at = ?, expires_at = ? where id = ?`,
				rfc3339(now), rfc3339(now.Add(session.Lifetime)), u.SessionID)
			return err
		})
	}
	return u, true
}

// requireUser resolves the session or writes 401.
func (s *server) requireUser(w http.ResponseWriter, r *http.Request) (session.User, bool) {
	u, ok := s.lookupSession(w, r)
	if !ok {
		httpError(w, http.StatusUnauthorized, "not authenticated")
		return session.User{}, false
	}
	return u, true
}

func (s *server) createSessionTx(ctx context.Context, tx *sql.Tx, userID int64, now time.Time) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		token, err := newSessionToken()
		if err != nil {
			return "", err
		}
		_, err = tx.ExecContext(ctx, `
insert into sessions(user_id, token_hash, created_at, expires_at, last_seen_at)
values(?, ?, ?, ?, ?)`,
			userID, hashSessionToken(token), rfc3339(now), rfc3339(now.Add(session.Lifetime)), rfc3339(now))
		if err == nil {
			return token, nil
		}
	}
	return "", errors.New("could not create session")
}

// ---- response shapes ----

type meResponse struct {
	UserID   int64   `json:"user_id"`
	Username *string `json:"username"`
	Telegram *string `json:"telegram"`
}

func meOf(u session.User) meResponse {
	resp := meResponse{UserID: u.UserID}
	if u.Username.Valid {
		resp.Username = &u.Username.String
	}
	if u.Telegram.Valid {
		resp.Telegram = &u.Telegram.String
	}
	return resp
}

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, meOf(u))
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if u, ok := s.lookupSession(w, r); ok {
		_ = s.withWriteTx(r.Context(), "logout", func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `delete from sessions where id = ?`, u.SessionID)
			return err
		})
	}
	session.ClearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// ---- registration ----

type registerStartRequest struct {
	InviteCode string `json:"invite_code"`
}
type registerStartResponse struct {
	Code      string `json:"code"`
	ExpiresAt string `json:"expires_at"`
}

func (s *server) handleRegisterStart(w http.ResponseWriter, r *http.Request) {
	var req registerStartRequest
	if !readJSON(w, r, &req) {
		return
	}
	code := strings.TrimSpace(req.InviteCode)
	if code == "" {
		httpError(w, http.StatusBadRequest, "invite code required")
		return
	}
	var out registerStartResponse
	now := time.Now()
	err := s.withWriteTx(r.Context(), "register-start", func(ctx context.Context, tx *sql.Tx) error {
		var inviteID int64
		var usedBy sql.NullInt64
		var expiresStr string
		row := tx.QueryRowContext(ctx, `select id, used_by, expires_at from invites where code = ?`, code)
		if err := row.Scan(&inviteID, &usedBy, &expiresStr); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errBadRequest("неверный код приглашения")
			}
			return err
		}
		if usedBy.Valid {
			return errBadRequest("код приглашения уже использован")
		}
		if expires, _ := time.Parse(time.RFC3339, expiresStr); now.After(expires) {
			return errBadRequest("срок действия кода истёк")
		}
		regCode, err := newTelegramAuthCode()
		if err != nil {
			return err
		}
		expiresAt := now.Add(session.TelegramAuthLifetime)
		_, err = tx.ExecContext(ctx, `
insert into telegram_login_codes(code, kind, invite_id, created_at, expires_at)
values(?, 'register', ?, ?, ?)`, regCode, inviteID, rfc3339(now), rfc3339(expiresAt))
		if err != nil {
			return err
		}
		out = registerStartResponse{Code: regCode, ExpiresAt: rfc3339(expiresAt)}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	writeJSON(w, out)
}

type registerStatusResponse struct {
	Status   string  `json:"status"`
	Username *string `json:"username,omitempty"`
}

// handleRegisterStatus polls a pending registration. Once the bot has consumed
// the code (filling in telegram_user_id), it provisions the user, marks the
// invite used, creates a session, and returns status=ready.
func (s *server) handleRegisterStatus(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		httpError(w, http.StatusBadRequest, "code required")
		return
	}
	now := time.Now()
	var (
		status   = "pending"
		username *string
		token    string
	)
	err := s.withWriteTx(r.Context(), "register-status", func(ctx context.Context, tx *sql.Tx) error {
		var (
			id          int64
			inviteID    sql.NullInt64
			tgUserID    sql.NullInt64
			tgUsername  sql.NullString
			expiresStr  string
			consumedStr sql.NullString
		)
		row := tx.QueryRowContext(ctx, `
select id, invite_id, telegram_user_id, telegram_username, expires_at, consumed_at
from telegram_login_codes where code = ? and kind = 'register'`, code)
		if err := row.Scan(&id, &inviteID, &tgUserID, &tgUsername, &expiresStr, &consumedStr); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				status = "not_found"
				return nil
			}
			return err
		}
		if !consumedStr.Valid || !tgUserID.Valid {
			if expires, _ := time.Parse(time.RFC3339, expiresStr); now.After(expires) {
				status = "expired"
			}
			return nil
		}
		// Provision (or fetch existing) user keyed by telegram_user_id.
		uid, uname, err := upsertTelegramUser(ctx, tx, tgUserID.Int64, tgUsername, now)
		if err != nil {
			return err
		}
		if inviteID.Valid {
			if _, err := tx.ExecContext(ctx, `
update invites set used_by = ?, used_at = ? where id = ? and used_by is null`,
				uid, rfc3339(now), inviteID.Int64); err != nil {
				return err
			}
		}
		token, err = s.createSessionTx(ctx, tx, uid, now)
		if err != nil {
			return err
		}
		status = "ready"
		if uname.Valid {
			username = &uname.String
		} else if tgUsername.Valid {
			username = &tgUsername.String
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	if token != "" {
		session.SetCookie(w, token)
	}
	writeJSON(w, registerStatusResponse{Status: status, Username: username})
}

func upsertTelegramUser(ctx context.Context, tx *sql.Tx, tgUserID int64, tgUsername sql.NullString, now time.Time) (int64, sql.NullString, error) {
	var (
		uid   int64
		uname sql.NullString
	)
	row := tx.QueryRowContext(ctx, `select id, username from users where telegram_user_id = ?`, tgUserID)
	err := row.Scan(&uid, &uname)
	if err == nil {
		_, uerr := tx.ExecContext(ctx, `update users set telegram_username = ?, updated_at = ? where id = ?`,
			tgUsername, rfc3339(now), uid)
		return uid, uname, uerr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, uname, err
	}
	res, err := tx.ExecContext(ctx, `
insert into users(telegram_user_id, telegram_username, created_at, updated_at)
values(?, ?, ?, ?)`, tgUserID, tgUsername, rfc3339(now), rfc3339(now))
	if err != nil {
		return 0, uname, err
	}
	uid, err = res.LastInsertId()
	return uid, sql.NullString{}, err
}

// ---- login ----

type loginStartRequest struct {
	Username string `json:"username"`
	SendCode bool   `json:"send_code"`
}
type loginStartResponse struct {
	Username    string `json:"username"`
	HasPassword bool   `json:"has_password"`
	CodeSent    bool   `json:"code_sent"`
	ExpiresAt   string `json:"expires_at,omitempty"`
}

func (s *server) handleLoginStart(w http.ResponseWriter, r *http.Request) {
	var req loginStartRequest
	if !readJSON(w, r, &req) {
		return
	}
	uname := strings.TrimSpace(req.Username)
	if uname == "" {
		httpError(w, http.StatusBadRequest, "логин обязателен")
		return
	}
	now := time.Now()
	var out loginStartResponse
	out.Username = uname
	err := s.withWriteTx(r.Context(), "login-start", func(ctx context.Context, tx *sql.Tx) error {
		var (
			uid      int64
			pwHash   sql.NullString
			tgUserID sql.NullInt64
		)
		row := tx.QueryRowContext(ctx, `select id, password_hash, telegram_user_id from users where username = ?`, uname)
		if err := row.Scan(&uid, &pwHash, &tgUserID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errBadRequest("пользователь не найден")
			}
			return err
		}
		out.HasPassword = pwHash.Valid && pwHash.String != ""
		// Issue a telegram login code if requested or if no password is set.
		if req.SendCode || !out.HasPassword {
			if !tgUserID.Valid {
				return errBadRequest("нет привязанного телеграма для входа по коду")
			}
			code, err := newTelegramLoginCode()
			if err != nil {
				return err
			}
			expiresAt := now.Add(session.TelegramAuthLifetime)
			_, err = tx.ExecContext(ctx, `
insert into telegram_login_codes(code, kind, user_id, telegram_user_id, created_at, expires_at)
values(?, 'login', ?, ?, ?, ?)`, code, uid, tgUserID.Int64, rfc3339(now), rfc3339(expiresAt))
			if err != nil {
				return err
			}
			out.CodeSent = true
			out.ExpiresAt = rfc3339(expiresAt)
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	writeJSON(w, out)
}

type loginCodeRequest struct {
	Code string `json:"code"`
}

func (s *server) handleLoginCode(w http.ResponseWriter, r *http.Request) {
	var req loginCodeRequest
	if !readJSON(w, r, &req) {
		return
	}
	code := strings.ToUpper(strings.TrimSpace(req.Code))
	if code == "" {
		httpError(w, http.StatusBadRequest, "код обязателен")
		return
	}
	now := time.Now()
	var (
		token string
		me    meResponse
	)
	err := s.withWriteTx(r.Context(), "login-code", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
update telegram_login_codes set consumed_at = ?
where code = ? and kind = 'login' and consumed_at is null and expires_at > ?`,
			rfc3339(now), code, rfc3339(now))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return errBadRequest("неверный или просроченный код")
		}
		var uid int64
		var uname, tg sql.NullString
		row := tx.QueryRowContext(ctx, `
select u.id, u.username, u.telegram_username
from telegram_login_codes c join users u on u.id = c.user_id
where c.code = ?`, code)
		if err := row.Scan(&uid, &uname, &tg); err != nil {
			return err
		}
		token, err = s.createSessionTx(ctx, tx, uid, now)
		if err != nil {
			return err
		}
		me = meOf(session.User{UserID: uid, Username: uname, Telegram: tg})
		return nil
	})
	if handleErr(w, err) {
		return
	}
	session.SetCookie(w, token)
	writeJSON(w, me)
}

type loginPasswordRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *server) handleLoginPassword(w http.ResponseWriter, r *http.Request) {
	var req loginPasswordRequest
	if !readJSON(w, r, &req) {
		return
	}
	uname := strings.TrimSpace(req.Username)
	if uname == "" || req.Password == "" {
		httpError(w, http.StatusBadRequest, "логин и пароль обязательны")
		return
	}
	now := time.Now()
	var (
		token string
		me    meResponse
	)
	err := s.withWriteTx(r.Context(), "login-password", func(ctx context.Context, tx *sql.Tx) error {
		var uid int64
		var pwHash, uname2, tg sql.NullString
		row := tx.QueryRowContext(ctx, `select id, password_hash, username, telegram_username from users where username = ?`, uname)
		if err := row.Scan(&uid, &pwHash, &uname2, &tg); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errBadRequest("неверный логин или пароль")
			}
			return err
		}
		if !verifyPassword(pwHash.String, req.Password) {
			return errBadRequest("неверный логин или пароль")
		}
		var err error
		token, err = s.createSessionTx(ctx, tx, uid, now)
		if err != nil {
			return err
		}
		me = meOf(session.User{UserID: uid, Username: uname2, Telegram: tg})
		return nil
	})
	if handleErr(w, err) {
		return
	}
	session.SetCookie(w, token)
	writeJSON(w, me)
}

// ---- username / password management ----

type usernameRequest struct {
	Username string `json:"username"`
}

func (s *server) handleSetUsername(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req usernameRequest
	if !readJSON(w, r, &req) {
		return
	}
	uname := strings.TrimSpace(req.Username)
	if len(uname) < 3 {
		httpError(w, http.StatusBadRequest, "логин слишком короткий")
		return
	}
	err := s.withWriteTx(r.Context(), "set-username", func(ctx context.Context, tx *sql.Tx) error {
		var existing sql.NullString
		if err := tx.QueryRowContext(ctx, `select username from users where id = ?`, u.UserID).Scan(&existing); err != nil {
			return err
		}
		if existing.Valid && existing.String != "" {
			return errBadRequest("логин уже задан")
		}
		_, err := tx.ExecContext(ctx, `update users set username = ?, updated_at = ? where id = ?`,
			uname, rfc3339(time.Now()), u.UserID)
		if err != nil && strings.Contains(err.Error(), "UNIQUE") {
			return errBadRequest("логин занят")
		}
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type passwordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *server) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req passwordRequest
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.NewPassword) < passwordMinLen || len(req.NewPassword) > passwordMaxLen {
		httpError(w, http.StatusBadRequest, "пароль должен быть от 8 до 72 символов")
		return
	}
	newHash, err := hashPassword(req.NewPassword)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "ошибка сервера")
		return
	}
	err = s.withWriteTx(r.Context(), "set-password", func(ctx context.Context, tx *sql.Tx) error {
		var cur sql.NullString
		if err := tx.QueryRowContext(ctx, `select password_hash from users where id = ?`, u.UserID).Scan(&cur); err != nil {
			return err
		}
		if cur.Valid && cur.String != "" {
			if !verifyPassword(cur.String, req.CurrentPassword) {
				return errBadRequest("неверный текущий пароль")
			}
		}
		_, err := tx.ExecContext(ctx, `update users set password_hash = ?, updated_at = ? where id = ?`,
			newHash, rfc3339(time.Now()), u.UserID)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- telegram bridge (shared-secret) ----

func botSecretOK(r *http.Request) (bool, bool) {
	secret := os.Getenv("XY_BOT_SECRET")
	if secret == "" {
		return false, false // not configured
	}
	got := r.Header.Get("X-Bot-Secret")
	return subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1, true
}

type telegramRegisterRequest struct {
	Code             string `json:"code"`
	TelegramUserID   int64  `json:"telegram_user_id"`
	TelegramUsername string `json:"telegram_username"`
}
type telegramMessageResponse struct {
	Message string `json:"message"`
}

func (s *server) handleTelegramRegister(w http.ResponseWriter, r *http.Request) {
	ok, configured := botSecretOK(r)
	if !configured {
		httpError(w, http.StatusServiceUnavailable, "bot bridge not configured")
		return
	}
	if !ok {
		httpError(w, http.StatusUnauthorized, "bad secret")
		return
	}
	var req telegramRegisterRequest
	if !readJSON(w, r, &req) {
		return
	}
	code := strings.TrimSpace(req.Code)
	now := time.Now()
	var msg string
	err := s.withWriteTx(r.Context(), "tg-register", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
update telegram_login_codes
set telegram_user_id = ?, telegram_username = ?, consumed_at = ?
where code = ? and kind = 'register' and consumed_at is null and expires_at > ?`,
			req.TelegramUserID, nullStr(req.TelegramUsername), rfc3339(now), code, rfc3339(now))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			msg = "Готово! Вернись на сайт — там уже видна твоя регистрация."
		} else {
			msg = "Код не найден или истёк. Начни регистрацию на сайте заново."
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	writeJSON(w, telegramMessageResponse{Message: msg})
}

type telegramLoginRequest struct {
	TelegramUserID   int64  `json:"telegram_user_id"`
	TelegramUsername string `json:"telegram_username"`
}

func (s *server) handleTelegramLogin(w http.ResponseWriter, r *http.Request) {
	ok, configured := botSecretOK(r)
	if !configured {
		httpError(w, http.StatusServiceUnavailable, "bot bridge not configured")
		return
	}
	if !ok {
		httpError(w, http.StatusUnauthorized, "bad secret")
		return
	}
	var req telegramLoginRequest
	if !readJSON(w, r, &req) {
		return
	}
	now := time.Now()
	var msg string
	err := s.withWriteTx(r.Context(), "tg-login", func(ctx context.Context, tx *sql.Tx) error {
		var uid int64
		row := tx.QueryRowContext(ctx, `select id from users where telegram_user_id = ?`, req.TelegramUserID)
		if err := row.Scan(&uid); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				msg = "Сначала зарегистрируйся на сайте по коду-приглашению."
				return nil
			}
			return err
		}
		code, err := newTelegramLoginCode()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
insert into telegram_login_codes(code, kind, user_id, telegram_user_id, telegram_username, created_at, expires_at)
values(?, 'login', ?, ?, ?, ?, ?)`,
			code, uid, req.TelegramUserID, nullStr(req.TelegramUsername), rfc3339(now), rfc3339(now.Add(session.TelegramAuthLifetime)))
		if err != nil {
			return err
		}
		msg = "Твой код для входа:\n" + code + "\nВведи его на странице входа в течение минуты."
		return nil
	})
	if handleErr(w, err) {
		return
	}
	writeJSON(w, telegramMessageResponse{Message: msg})
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
