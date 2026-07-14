package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"pecheny.me/dopecore/tgbridge"

	"pecheny.me/dopecore/sqlitex"

	"pecheny.me/dopecore/authcred"

	"pecheny.me/dopecore/session"
)

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// ---- session lookup / middleware ----

// lookupSession resolves the session cookie to a user, refreshing the sliding
// expiry at most once a minute. Returns ok=false if no valid session.
func (s *server) lookupSession(w http.ResponseWriter, r *http.Request) (session.User, bool) {
	c, err := r.Cookie(session.CookieName)
	if err != nil || c.Value == "" {
		return session.User{}, false
	}
	hash := authcred.HashSessionToken(c.Value)
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
	lastSeen, _ := time.Parse(time.RFC3339, lastSeenStr)
	if authcred.NeedsRefresh(lastSeen, expires, now) {
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
	return authcred.CreateSession(ctx, tx, userID, now)
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
		regCode, err := authcred.NewTelegramAuthCode()
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
			code, err := authcred.NewTelegramLoginCode()
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
		if !authcred.VerifyPassword(pwHash.String, req.Password) {
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
	if len(uname) > 64 {
		httpError(w, http.StatusBadRequest, "логин слишком длинный")
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
		if sqlitex.IsUniqueViolation(err) {
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
	if len(req.NewPassword) < authcred.PasswordMinLen || len(req.NewPassword) > authcred.PasswordMaxLen {
		httpError(w, http.StatusBadRequest, "пароль должен быть от 8 до 72 символов")
		return
	}
	newHash, err := authcred.HashPassword(req.NewPassword)
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
			if !authcred.VerifyPassword(cur.String, req.CurrentPassword) {
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

// requireBotSecret gates the bridge on XY_BOT_SECRET. An unset secret disables
// the bridge outright rather than leaving the code-issuing endpoints open.
func (s *server) requireBotSecret(w http.ResponseWriter, r *http.Request) bool {
	ok, configured := tgbridge.SecretOK(r, os.Getenv("XY_BOT_SECRET"))
	switch {
	case !configured:
		httpError(w, http.StatusServiceUnavailable, "bot bridge not configured")
	case !ok:
		httpError(w, http.StatusUnauthorized, "bad secret")
	}
	return ok && configured
}

func (s *server) handleTelegramRegister(w http.ResponseWriter, r *http.Request) {
	if !s.requireBotSecret(w, r) {
		return
	}
	var req tgbridge.RegisterRequest
	if !readJSON(w, r, &req) {
		return
	}
	code := strings.TrimSpace(req.Code)
	now := time.Now()
	var msg string
	err := s.withWriteTx(r.Context(), "tg-register", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, tgbridge.ConsumeRegisterSQL,
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
	writeJSON(w, tgbridge.Response{Message: msg})
}

func (s *server) handleTelegramLogin(w http.ResponseWriter, r *http.Request) {
	if !s.requireBotSecret(w, r) {
		return
	}
	var req tgbridge.LoginRequest
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
		code, err := authcred.NewTelegramLoginCode()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, tgbridge.IssueLoginSQL,
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
	writeJSON(w, tgbridge.Response{Message: msg})
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
