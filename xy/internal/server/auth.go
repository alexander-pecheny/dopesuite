package server

import (
	"context"
	"database/sql"
	"encoding/json"
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
		// Slide the browser cookie's MaxAge with the server session, else the
		// cookie dies 30 days after login however active the user is.
		session.SetCookie(w, c.Value)
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
	// Display preferences, editable on /profile: the board layout (see
	// handleSetSizes) and the author name pre-filled into new question cards
	// (see handleSetDefaultAuthor), and which field a card's list preview
	// derives its title from (see handleSetCardTitle).
	Sizes         json.RawMessage `json:"sizes,omitempty"`
	DefaultAuthor string          `json:"default_author,omitempty"`
	CardTitle     string          `json:"card_title,omitempty"`
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
	resp := meOf(u)
	var sizes, author, cardTitle sql.NullString
	if err := s.db.QueryRowContext(r.Context(), `select sizes, default_author, card_title from users where id = ?`, u.UserID).Scan(&sizes, &author, &cardTitle); handleErr(w, err) {
		return
	}
	if sizes.Valid && sizes.String != "" {
		resp.Sizes = json.RawMessage(sizes.String)
	}
	resp.DefaultAuthor = author.String
	resp.CardTitle = cardTitle.String
	writeJSON(w, resp)
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

// ---- telegram login / registration (one handshake) ----

type tgStartResponse struct {
	Code      string `json:"code"`
	ExpiresAt string `json:"expires_at"`
}

// handleTgStart mints a bot code for the telegram handshake. The visitor sends it
// to the bot; handleTgStatus then resolves who they are — no username needed up
// front (that comes later, and only for a brand-new telegram account).
func (s *server) handleTgStart(w http.ResponseWriter, r *http.Request) {
	var out tgStartResponse
	now := time.Now()
	err := s.withWriteTx(r.Context(), "tg-start", func(ctx context.Context, tx *sql.Tx) error {
		// Opportunistically reap lapsed codes so consumed-but-abandoned rows don't
		// linger as replay fodder.
		if _, err := tx.ExecContext(ctx, `delete from telegram_login_codes where expires_at < ?`, rfc3339(now)); err != nil {
			return err
		}
		code, err := authcred.NewTelegramAuthCode()
		if err != nil {
			return err
		}
		expiresAt := now.Add(session.TelegramAuthLifetime)
		if _, err := tx.ExecContext(ctx, `
insert into telegram_login_codes(code, kind, created_at, expires_at)
values(?, 'register', ?, ?)`, code, rfc3339(now), rfc3339(expiresAt)); err != nil {
			return err
		}
		out = tgStartResponse{Code: code, ExpiresAt: rfc3339(expiresAt)}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	writeJSON(w, out)
}

type tgStatusResponse struct {
	Status   string  `json:"status"`
	Username *string `json:"username,omitempty"`
}

// handleTgStatus polls the handshake. Once the bot fills in the telegram identity,
// a known telegram account logs straight in (ready); a brand-new one returns
// choose_username, and its account is created later by handleTgClaim.
func (s *server) handleTgStatus(w http.ResponseWriter, r *http.Request) {
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
	err := s.withWriteTx(r.Context(), "tg-status", func(ctx context.Context, tx *sql.Tx) error {
		var (
			tgUserID    sql.NullInt64
			tgUsername  sql.NullString
			tgName      sql.NullString
			expiresStr  string
			consumedStr sql.NullString
		)
		row := tx.QueryRowContext(ctx, `
select telegram_user_id, telegram_username, telegram_name, expires_at, consumed_at
from telegram_login_codes where code = ? and kind = 'register'`, code)
		if err := row.Scan(&tgUserID, &tgUsername, &tgName, &expiresStr, &consumedStr); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				status = "not_found"
				return nil
			}
			return err
		}
		// Expiry bounds the whole handshake, consumed or not — so a code leaked
		// via the status URL can't be replayed into a session once it lapses.
		if expires, _ := time.Parse(time.RFC3339, expiresStr); now.After(expires) {
			status = "expired"
			return nil
		}
		if !consumedStr.Valid || !tgUserID.Valid {
			return nil // pending
		}
		var euid int64
		var euname sql.NullString
		switch err := tx.QueryRowContext(ctx, `select id, username from users where telegram_user_id = ?`, tgUserID.Int64).Scan(&euid, &euname); {
		case err == nil:
			if _, err := tx.ExecContext(ctx, `update users set telegram_username = ?, telegram_name = ?, updated_at = ? where id = ?`,
				tgUsername, tgName, rfc3339(now), euid); err != nil {
				return err
			}
			var terr error
			if token, terr = s.createSessionTx(ctx, tx, euid, now); terr != nil {
				return terr
			}
			if _, err := tx.ExecContext(ctx, `delete from telegram_login_codes where code = ?`, code); err != nil {
				return err
			}
			status, username = "ready", firstNonNull(euname, tgUsername)
		case errors.Is(err, sql.ErrNoRows):
			status = "choose_username"
		default:
			return err
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	if token != "" {
		session.SetCookie(w, token)
	}
	writeJSON(w, tgStatusResponse{Status: status, Username: username})
}

// handleTgClaim finishes a brand-new telegram account: the visitor picks a
// username. Free → create + log in. An existing password account → link it once
// they prove the password (password_required until they do). Taken by another
// telegram account → username_taken.
func (s *server) handleTgClaim(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code     string `json:"code"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	code := strings.TrimSpace(req.Code)
	uname := strings.TrimSpace(req.Username)
	if !validNewUsername(uname) {
		httpError(w, http.StatusBadRequest, "логин: 3–64 символа, латиница, цифры, . _ -")
		return
	}
	now := time.Now()
	var (
		status   string
		username *string
		token    string
	)
	err := s.withWriteTx(r.Context(), "tg-claim", func(ctx context.Context, tx *sql.Tx) error {
		var (
			tgUserID   sql.NullInt64
			tgUsername sql.NullString
			tgName     sql.NullString
		)
		row := tx.QueryRowContext(ctx, `
select telegram_user_id, telegram_username, telegram_name
from telegram_login_codes
where code = ? and kind = 'register' and consumed_at is not null and expires_at > ?`, code, rfc3339(now))
		if err := row.Scan(&tgUserID, &tgUsername, &tgName); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errBadRequest("код не найден, начните заново")
			}
			return err
		}
		if !tgUserID.Valid {
			return errBadRequest("код не найден, начните заново")
		}
		// delCode burns the code on any successful login/link so it can't be replayed.
		delCode := func() error {
			_, e := tx.ExecContext(ctx, `delete from telegram_login_codes where code = ?`, code)
			return e
		}
		// This telegram may already resolve to an account (double-submit / race).
		var euid int64
		var euname sql.NullString
		switch err := tx.QueryRowContext(ctx, `select id, username from users where telegram_user_id = ?`, tgUserID.Int64).Scan(&euid, &euname); {
		case err == nil:
			if token, err = s.createSessionTx(ctx, tx, euid, now); err != nil {
				return err
			}
			if err := delCode(); err != nil {
				return err
			}
			status, username = "ready", firstNonNull(euname, tgUsername)
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		var uid int64
		var pwHash sql.NullString
		switch err := tx.QueryRowContext(ctx, `select id, password_hash from users where username = ?`, uname).Scan(&uid, &pwHash); {
		case errors.Is(err, sql.ErrNoRows):
			if isAdminUsername(uname) {
				return errForbidden("этот логин зарезервирован")
			}
			res, ierr := tx.ExecContext(ctx, `
insert into users(telegram_user_id, telegram_username, telegram_name, username, created_at, updated_at)
values(?, ?, ?, ?, ?, ?)`, tgUserID.Int64, tgUsername, tgName, uname, rfc3339(now), rfc3339(now))
			if sqlitex.IsUniqueViolation(ierr) {
				status, username = "username_taken", &uname
				return nil
			}
			if ierr != nil {
				return ierr
			}
			nid, _ := res.LastInsertId()
			if token, ierr = s.createSessionTx(ctx, tx, nid, now); ierr != nil {
				return ierr
			}
			if ierr := delCode(); ierr != nil {
				return ierr
			}
			status, username = "ready", &uname
		case err != nil:
			return err
		case pwHash.Valid && pwHash.String != "":
			// Existing password account: link only once the password is proven.
			if req.Password == "" {
				status, username = "password_required", &uname
				return nil
			}
			if !authcred.VerifyPassword(pwHash.String, req.Password) {
				return errBadRequest("неверный пароль")
			}
			if _, err := tx.ExecContext(ctx, `
update users set telegram_user_id = ?, telegram_username = ?, telegram_name = ?, updated_at = ? where id = ?`,
				tgUserID.Int64, tgUsername, tgName, rfc3339(now), uid); err != nil {
				if sqlitex.IsUniqueViolation(err) {
					return errBadRequest("этот телеграм уже привязан к другому аккаунту")
				}
				return err
			}
			if token, err = s.createSessionTx(ctx, tx, uid, now); err != nil {
				return err
			}
			if err := delCode(); err != nil {
				return err
			}
			status, username = "ready", &uname
		default:
			status, username = "username_taken", &uname
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	if token != "" {
		session.SetCookie(w, token)
	}
	writeJSON(w, tgStatusResponse{Status: status, Username: username})
}

// firstNonNull returns a pointer to the first valid string, else nil.
func firstNonNull(vals ...sql.NullString) *string {
	for _, v := range vals {
		if v.Valid && v.String != "" {
			s := v.String
			return &s
		}
	}
	return nil
}

// ---- login (password; telegram login goes through the tg handshake above) ----

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

// ---- display prefs ----

// displaySizes is the per-user board layout ({boardW,listW,cardLines}), shared
// across all of the user's boards and devices. Display numbers only — no question
// content — so it lives plaintext in users.sizes, like ranks (see migrateV9). All
// three are pointers so a null (boardW/cardLines "unlimited") round-trips and an
// absent field doesn't collapse to a spurious zero; the client clamps ranges on
// read, so the server only validates the shape.
type displaySizes struct {
	BoardW    *int `json:"boardW"`
	ListW     *int `json:"listW"`
	CardLines *int `json:"cardLines"`
	CardFont  *int `json:"cardFont"`
}

func (s *server) handleSetSizes(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var sz displaySizes
	if !readJSON(w, r, &sz) {
		return
	}
	// Re-marshal to a canonical {boardW,listW,cardLines}, dropping anything else.
	canon, err := json.Marshal(sz)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid sizes")
		return
	}
	err = s.withWriteTx(r.Context(), "set-sizes", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update users set sizes = ?, updated_at = ? where id = ?`,
			string(canon), rfc3339(time.Now()), u.UserID)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSetDefaultAuthor stores the author name pre-filled into new question
// cards (users.default_author, see migrateV11). Empty clears it.
func (s *server) handleSetDefaultAuthor(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		DefaultAuthor string `json:"default_author"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	author := strings.TrimSpace(req.DefaultAuthor)
	if len(author) > 200 {
		httpError(w, http.StatusBadRequest, "слишком длинное имя")
		return
	}
	err := s.withWriteTx(r.Context(), "set-default-author", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update users set default_author = ?, updated_at = ? where id = ?`,
			author, rfc3339(time.Now()), u.UserID)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// cardTitleModes allowlists the values of users.card_title (see migrateV13):
// which field a card's list preview derives its title from. "" means the
// default, "question".
var cardTitleModes = map[string]bool{"": true, "question": true, "answer": true}

// handleSetCardTitle stores whether card previews show the question text or the
// answer (users.card_title, see migrateV13). A card's alias, when set, wins over
// either.
func (s *server) handleSetCardTitle(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		CardTitle string `json:"card_title"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	mode := strings.TrimSpace(req.CardTitle)
	if !cardTitleModes[mode] {
		httpError(w, http.StatusBadRequest, "bad card_title")
		return
	}
	err := s.withWriteTx(r.Context(), "set-card-title", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update users set card_title = ?, updated_at = ? where id = ?`,
			mode, rfc3339(time.Now()), u.UserID)
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
			req.TelegramUserID, nullStr(req.TelegramUsername), nullStr(req.TelegramName), rfc3339(now), code, rfc3339(now))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			msg = "Готово! Вернись на сайт — вход подтверждён."
		} else {
			msg = "Код не найден или истёк. Начни вход на сайте заново."
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	writeJSON(w, tgbridge.Response{Message: msg})
}

// handleTelegramLogin answers /start and /login sent to the bot. Login now begins
// on the website (which mints the code the user forwards here), so there is no
// server-issued login code to hand back — just point them at /login.
func (s *server) handleTelegramLogin(w http.ResponseWriter, r *http.Request) {
	if !s.requireBotSecret(w, r) {
		return
	}
	var req tgbridge.LoginRequest
	if !readJSON(w, r, &req) {
		return
	}
	writeJSON(w, tgbridge.Response{Message: "Чтобы войти, открой https://xy.pecheny.me/login и нажми «Войти через телеграм» — сайт выдаст код, пришли его мне."})
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
