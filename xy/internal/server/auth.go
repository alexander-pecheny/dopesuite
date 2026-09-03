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

	"pecheny.me/dopecore/sqlitex"

	"pecheny.me/dopecore/authcred"

	"pecheny.me/dopecore/session"
	"pecheny.me/dopecore/tglogin"

	xystrings "xy/i18nstrings"
)

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// ---- session lookup / middleware ----

// xySessions is authcred's session store over xy's users table.
var xySessions = authcred.Sessions{
	UserColumns: "u.username, u.telegram_username",
	UserDest:    func(u *session.User) ([]any, func()) { return []any{&u.Username, &u.Telegram}, nil },
}

// bearerToken returns the raw API token of an `Authorization: Bearer …` header,
// or "" when the request carries none.
func bearerToken(r *http.Request) string {
	const prefix = "bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

// lookupSession resolves a request to its user: an API token when the request
// carries one, the session cookie otherwise. A token IS the user (ADR-0015) —
// bar the routes requireCookieUser guards.
func (s *server) lookupSession(w http.ResponseWriter, r *http.Request) (session.User, bool) {
	if raw := bearerToken(r); raw != "" {
		return s.lookupAPIToken(r, raw)
	}
	return s.lookupCookieSession(w, r)
}

// tokenState is what an API token resolves to: its row id and owner, plus why
// it was refused. One resolver serves both callers — this one and the
// Trello-compatible API's key+token params (trello_compat.go).
type tokenState int

const (
	tokenOK tokenState = iota
	tokenUnknown
	tokenExpired
)

// resolveAPIToken looks a raw token up by hash. It reads the user's display
// columns too, so an authenticated request costs one statement.
func (s *server) resolveAPIToken(ctx context.Context, raw string) (u session.User, id int64, state tokenState, err error) {
	var (
		expires  string
		revoked  sql.NullString
		lastUsed sql.NullString
	)
	err = s.db.QueryRowContext(ctx, `
select t.id, t.user_id, t.expires_at, t.revoked_at, t.last_used_at, u.username, u.telegram_username
from api_tokens t join users u on u.id = t.user_id
where t.token_hash = ?`, authcred.HashSessionToken(raw)).
		Scan(&id, &u.UserID, &expires, &revoked, &lastUsed, &u.Username, &u.Telegram)
	switch {
	case errors.Is(err, sql.ErrNoRows), revoked.Valid && err == nil:
		return session.User{}, 0, tokenUnknown, nil
	case err != nil:
		return session.User{}, 0, tokenUnknown, err
	}
	if exp, _ := time.Parse(time.RFC3339, expires); time.Now().After(exp) {
		return session.User{}, id, tokenExpired, nil
	}
	s.touchToken(ctx, id, lastUsed)
	return u, id, tokenOK, nil
}

// touchToken stamps last_used_at, so /profile/tokens shows which credential is
// live — but at most once a minute per token: a CLI takes the write lock on
// every request otherwise, and the answer this feeds is "today or not".
func (s *server) touchToken(ctx context.Context, id int64, lastUsed sql.NullString) {
	now := time.Now()
	if lastUsed.Valid {
		if seen, err := time.Parse(time.RFC3339, lastUsed.String); err == nil && now.Sub(seen) < time.Minute {
			return
		}
	}
	_ = s.withWriteTx(ctx, "token-touch", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update api_tokens set last_used_at = ? where id = ?`, rfc3339(now), id)
		return err
	})
}

// lookupAPIToken resolves a bearer API token to its owner, rejecting unknown,
// revoked and expired ones — as the cookie path does, a lookup failure is
// simply «not authenticated».
func (s *server) lookupAPIToken(r *http.Request, raw string) (session.User, bool) {
	u, _, state, err := s.resolveAPIToken(r.Context(), raw)
	return u, err == nil && state == tokenOK
}

// lookupCookieSession resolves the session cookie to a user; an expired session
// is deleted, a live one slides — with the browser cookie's MaxAge, else the
// cookie dies 30 days after login however active the user is.
func (s *server) lookupCookieSession(w http.ResponseWriter, r *http.Request) (session.User, bool) {
	c, err := r.Cookie(session.CookieName)
	if err != nil || c.Value == "" {
		return session.User{}, false
	}
	now := time.Now()
	u, state, slide := xySessions.Lookup(r.Context(), s.db, c.Value, now)
	switch state {
	case authcred.NoSession:
		return session.User{}, false
	case authcred.Expired:
		_ = s.withWriteTx(r.Context(), "session-expire", func(ctx context.Context, tx *sql.Tx) error {
			return authcred.DeleteSession(ctx, tx, u.SessionID)
		})
		return session.User{}, false
	}
	if slide {
		_ = s.withWriteTx(r.Context(), "session-refresh", func(ctx context.Context, tx *sql.Tx) error {
			return authcred.SlideSession(ctx, tx, u.SessionID, now)
		})
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

// requireCookieUser is requireUser for the three things an API token may not do:
// change the password (its own kill switch — see handleSetPassword), change the
// username, and reach /admin. Everything else a token does as the user.
func (s *server) requireCookieUser(w http.ResponseWriter, r *http.Request) (session.User, bool) {
	if bearerToken(r) != "" {
		httpError(w, http.StatusForbidden, xystrings.Default.Auth.Token.Forbidden())
		return session.User{}, false
	}
	u, ok := s.lookupCookieSession(w, r)
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
	// (see handleSetDefaultAuthor), which field a card's list preview
	// derives its title from (see handleSetCardTitle), and which kind of
	// timeline entry an opened card's timeline shows (see handleSetFeedDefault).
	Sizes         json.RawMessage `json:"sizes,omitempty"`
	DefaultAuthor string          `json:"default_author,omitempty"`
	CardTitle     string          `json:"card_title,omitempty"`
	FeedDefault   string          `json:"feed_default,omitempty"`
	// The test-session preferences, and the first-run stamp every page checks.
	Timezone         string          `json:"timezone,omitempty"`
	AnnounceCities   json.RawMessage `json:"announce_cities,omitempty"`
	SessionTitleMode string          `json:"session_title_mode,omitempty"`
	OnboardedAt      string          `json:"onboarded_at,omitempty"`
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

// userPrefs is every per-user display preference, read as one row. They ride
// both /api/auth/me and the board snapshot, and adding a seventh used to mean
// editing the select and its unpack in two places.
type userPrefs struct {
	Sizes            sql.NullString
	DefaultAuthor    sql.NullString
	CardTitle        sql.NullString
	FeedDefault      sql.NullString
	Timezone         sql.NullString
	AnnounceCities   sql.NullString
	SessionTitleMode sql.NullString
	OnboardedAt      sql.NullString
}

func loadUserPrefs(ctx context.Context, q rowQuerier, uid int64) (userPrefs, error) {
	var p userPrefs
	err := q.QueryRowContext(ctx, `
select sizes, default_author, card_title, feed_default, timezone, announce_cities, session_title_mode, onboarded_at
from users where id = ?`, uid).
		Scan(&p.Sizes, &p.DefaultAuthor, &p.CardTitle, &p.FeedDefault, &p.Timezone, &p.AnnounceCities, &p.SessionTitleMode, &p.OnboardedAt)
	return p, err
}

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	resp := meOf(u)
	p, err := loadUserPrefs(r.Context(), s.db, u.UserID)
	if handleErr(w, err) {
		return
	}
	if p.Sizes.Valid && p.Sizes.String != "" {
		resp.Sizes = json.RawMessage(p.Sizes.String)
	}
	if p.AnnounceCities.Valid && p.AnnounceCities.String != "" {
		resp.AnnounceCities = json.RawMessage(p.AnnounceCities.String)
	}
	resp.DefaultAuthor = p.DefaultAuthor.String
	resp.CardTitle = p.CardTitle.String
	resp.FeedDefault = p.FeedDefault.String
	resp.Timezone = p.Timezone.String
	resp.SessionTitleMode = p.SessionTitleMode.String
	resp.OnboardedAt = p.OnboardedAt.String
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
	Code        string `json:"code"`
	ExpiresAt   string `json:"expires_at"`
	BotUsername string `json:"bot_username,omitempty"`
}

// telegramConfigured reports whether this instance is SET UP for telegram login:
// a bot of its own, AND that bot's @handle, without which the login page has no
// one to send the visitor to. Half-configured is the state that hurts — it
// advertises a way in, then shows a dead link and a literal «@…», which is what
// an instance carrying only the token did for two weeks before anyone reported it.
func (s *server) telegramConfigured() bool {
	return s.bot != nil && botUsername() != ""
}

// The three answers the login page can get about telegram. Being configured and
// being usable are different things, and a visitor deserves to know which one
// failed rather than watching a code that no bot will ever collect.
const (
	tgStatusOK            = "ok"
	tgStatusMisconfigured = "misconfigured" // no bot on this instance, or no @handle
	tgStatusUnreachable   = "unreachable"   // configured, but the bot is not polling
)

func (s *server) telegramStatus() string {
	if !s.telegramConfigured() {
		return tgStatusMisconfigured
	}
	if !s.botPolling() {
		return tgStatusUnreachable
	}
	return tgStatusOK
}

// handleLoginMethods tells the login page which ways in to offer. `telegram` is
// kept for older pages, which read it as a bare on/off; `telegram_status` says
// WHICH kind of no it is, so the page can name the problem.
func (s *server) handleLoginMethods(w http.ResponseWriter, r *http.Request) {
	st := s.telegramStatus()
	writeJSON(w, map[string]any{"telegram": st == tgStatusOK, "telegram_status": st})
}

// botUsername is the login bot's @handle, used to build the t.me deep link the
// login page offers. Required for telegram login — see telegramConfigured.
func botUsername() string { return strings.TrimSpace(os.Getenv("XY_BOT_NAME")) }

// The Telegram handshake (start → status poll → claim) is dopecore/tglogin's
// state machine over xy's users table; these handlers keep xy's write
// transaction, its validation and its error text.
func (s *server) handshake() tglogin.Handshake { return tglogin.Handshake{Users: xyUsers{}} }

func (s *server) handleTgStart(w http.ResponseWriter, r *http.Request) {
	if s.bot == nil {
		httpError(w, http.StatusServiceUnavailable, "telegram login is not configured")
		return
	}
	var out tgStartResponse
	err := s.withWriteTx(r.Context(), "tg-start", func(ctx context.Context, tx *sql.Tx) error {
		res, err := s.handshake().Start(ctx, tx, time.Now())
		out = tgStartResponse{Code: res.Code, ExpiresAt: rfc3339(res.ExpiresAt), BotUsername: botUsername()}
		return err
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

func (s *server) handleTgStatus(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		httpError(w, http.StatusBadRequest, "code required")
		return
	}
	var out tglogin.Outcome
	err := s.withWriteTx(r.Context(), "tg-status", func(ctx context.Context, tx *sql.Tx) (err error) {
		out, err = s.handshake().Resolve(ctx, tx, code, time.Now())
		return err
	})
	writeOutcome(w, out, err)
}

func (s *server) handleTgClaim(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code     string `json:"code"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	uname := strings.TrimSpace(req.Username)
	if !validNewUsername(uname) {
		httpError(w, http.StatusBadRequest, xystrings.Default.Auth.Tg.UsernameFormat())
		return
	}
	var out tglogin.Outcome
	err := s.withWriteTx(r.Context(), "tg-claim", func(ctx context.Context, tx *sql.Tx) (err error) {
		out, err = s.handshake().Claim(ctx, tx, req.Code, uname, req.Password, time.Now())
		str := xystrings.Default
		switch {
		case errors.Is(err, tglogin.ErrCodeNotFound):
			return errBadRequest(str.Auth.Tg.CodeMissing())
		case errors.Is(err, tglogin.ErrWrongPassword):
			return errBadRequest(str.Auth.Tg.PasswordWrong())
		case errors.Is(err, tglogin.ErrTelegramLinked):
			return errBadRequest(str.Auth.Tg.TelegramTaken())
		}
		return err
	})
	writeOutcome(w, out, err)
}

func writeOutcome(w http.ResponseWriter, out tglogin.Outcome, err error) {
	if handleErr(w, err) {
		return
	}
	if out.Token != "" {
		session.SetCookie(w, out.Token)
	}
	writeJSON(w, tgStatusResponse{Status: out.Status, Username: out.Username})
}

// xyUsers is xy's users table as the handshake needs it.
type xyUsers struct{}

func (xyUsers) ByTelegram(ctx context.Context, tx tglogin.Tx, tg int64) (tglogin.Account, bool, error) {
	return scanAccount(tx.QueryRowContext(ctx, `select id, username, password_hash from users where telegram_user_id = ?`, tg))
}

func (xyUsers) ByUsername(ctx context.Context, tx tglogin.Tx, username string) (tglogin.Account, bool, error) {
	return scanAccount(tx.QueryRowContext(ctx, `select id, username, password_hash from users where username = ?`, username))
}

func scanAccount(row *sql.Row) (tglogin.Account, bool, error) {
	var a tglogin.Account
	switch err := row.Scan(&a.ID, &a.Username, &a.PasswordHash); {
	case errors.Is(err, sql.ErrNoRows):
		return a, false, nil
	case err != nil:
		return a, false, err
	}
	return a, true, nil
}

func (xyUsers) Create(ctx context.Context, tx tglogin.Tx, id tglogin.Identity, username string, now time.Time) (int64, error) {
	if isAdminUsername(username) {
		return 0, errForbidden(xystrings.Default.Auth.Tg.UsernameReserved())
	}
	res, err := tx.ExecContext(ctx, `
insert into users(telegram_user_id, telegram_username, telegram_name, username, created_at, updated_at)
values(?, ?, ?, ?, ?, ?)`, id.TelegramUserID, id.Username, id.Name, username, rfc3339(now), rfc3339(now))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (xyUsers) Attach(ctx context.Context, tx tglogin.Tx, userID int64, id tglogin.Identity, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
update users set telegram_user_id = ?, telegram_username = ?, telegram_name = ?, updated_at = ? where id = ?`,
		id.TelegramUserID, id.Username, id.Name, rfc3339(now), userID)
	return err
}

// ---- login (password; telegram login goes through the tg handshake above) ----

type loginPasswordRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *server) handleLoginPassword(w http.ResponseWriter, r *http.Request) {
	str := xystrings.Default
	var req loginPasswordRequest
	if !readJSON(w, r, &req) {
		return
	}
	uname := strings.TrimSpace(req.Username)
	if uname == "" || req.Password == "" {
		httpError(w, http.StatusBadRequest, str.Auth.Login.FieldsRequired())
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
				return errBadRequest(str.Auth.Login.Invalid())
			}
			return err
		}
		if ok, _, _ := authcred.VerifyPasswordUpgrading(pwHash.String, "", req.Password); !ok {
			return errBadRequest(str.Auth.Login.Invalid())
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
	str := xystrings.Default
	u, ok := s.requireCookieUser(w, r)
	if !ok {
		return
	}
	var req usernameRequest
	if !readJSON(w, r, &req) {
		return
	}
	uname := strings.TrimSpace(req.Username)
	if len(uname) < 3 {
		httpError(w, http.StatusBadRequest, str.Auth.Username.TooShort())
		return
	}
	if len(uname) > 64 {
		httpError(w, http.StatusBadRequest, str.Auth.Username.TooLong())
		return
	}
	err := s.withWriteTx(r.Context(), "set-username", func(ctx context.Context, tx *sql.Tx) error {
		var existing sql.NullString
		if err := tx.QueryRowContext(ctx, `select username from users where id = ?`, u.UserID).Scan(&existing); err != nil {
			return err
		}
		if existing.Valid && existing.String != "" {
			return errBadRequest(str.Auth.Username.AlreadySet())
		}
		_, err := tx.ExecContext(ctx, `update users set username = ?, updated_at = ? where id = ?`,
			uname, rfc3339(time.Now()), u.UserID)
		if sqlitex.IsUniqueViolation(err) {
			return errBadRequest(str.Auth.Username.Taken())
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

// handleSetPassword is also the kill switch: a new password revokes every API
// token and every other session of the account, so a leaked credential dies
// with one act the user already knows (ADR-0015). Hence cookie-only — a token
// must not be able to lock its owner out, nor to survive by minting siblings.
func (s *server) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	str := xystrings.Default
	u, ok := s.requireCookieUser(w, r)
	if !ok {
		return
	}
	var req passwordRequest
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.NewPassword) < authcred.PasswordMinLen || len(req.NewPassword) > authcred.PasswordMaxLen {
		httpError(w, http.StatusBadRequest, str.Auth.Password.Length())
		return
	}
	newHash, err := authcred.HashPassword(req.NewPassword)
	if err != nil {
		httpError(w, http.StatusInternalServerError, str.Server.Internal())
		return
	}
	err = s.withWriteTx(r.Context(), "set-password", func(ctx context.Context, tx *sql.Tx) error {
		var cur sql.NullString
		if err := tx.QueryRowContext(ctx, `select password_hash from users where id = ?`, u.UserID).Scan(&cur); err != nil {
			return err
		}
		if cur.Valid && cur.String != "" {
			if ok, _, _ := authcred.VerifyPasswordUpgrading(cur.String, "", req.CurrentPassword); !ok {
				return errBadRequest(str.Auth.Password.CurrentWrong())
			}
		}
		now := rfc3339(time.Now())
		if _, err := tx.ExecContext(ctx, `update users set password_hash = ?, updated_at = ? where id = ?`,
			newHash, now, u.UserID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`update api_tokens set revoked_at = ? where user_id = ? and revoked_at is null`,
			now, u.UserID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `delete from sessions where user_id = ? and id <> ?`, u.UserID, u.SessionID)
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
// content — so it lives plaintext in users.sizes, like ranks (see schema v9). All
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
// cards (users.default_author, see schema v11). Empty clears it.
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
		httpError(w, http.StatusBadRequest, xystrings.Default.Auth.Profile.NameTooLong())
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

// handleSetProfileDefaults stores the two questions the first-run modal asks —
// the IANA timezone a new test session is anchored in and the default author —
// and stamps onboarded_at so the modal never opens again. Also the write path
// for the /profile dialogs that edit either one on its own.
func (s *server) handleSetProfileDefaults(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Timezone         *string `json:"timezone"`
		DefaultAuthor    *string `json:"default_author"`
		SessionTitleMode *string `json:"session_title_mode"`
		Onboarded        bool    `json:"onboarded"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	// Not validated against the tz database: Intl on the client is the source of
	// these, and a zone the server rejects would be a zone the user's browser
	// believes in. Length-capped only.
	if req.Timezone != nil && len(*req.Timezone) > 64 {
		httpError(w, http.StatusBadRequest, xystrings.Default.Auth.Profile.TimezoneTooLong())
		return
	}
	if req.DefaultAuthor != nil && len(strings.TrimSpace(*req.DefaultAuthor)) > 200 {
		httpError(w, http.StatusBadRequest, xystrings.Default.Auth.Profile.NameTooLong())
		return
	}
	err := s.withWriteTx(r.Context(), "set-profile-defaults", func(ctx context.Context, tx *sql.Tx) error {
		var p patch
		if req.Timezone != nil {
			p.set("timezone", strings.TrimSpace(*req.Timezone))
		}
		if req.DefaultAuthor != nil {
			p.set("default_author", strings.TrimSpace(*req.DefaultAuthor))
		}
		if req.SessionTitleMode != nil {
			if !sessionTitleModes[*req.SessionTitleMode] {
				return errBadRequest("bad session_title_mode")
			}
			p.set("session_title_mode", *req.SessionTitleMode)
		}
		if err := p.apply(ctx, tx, "users", u.UserID); err != nil {
			return err
		}
		if req.Onboarded {
			if _, err := tx.ExecContext(ctx,
				`update users set onboarded_at = ? where id = ? and onboarded_at is null`, rfc3339(time.Now()), u.UserID); err != nil {
				return err
			}
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSetAnnounceCities stores the caller's default announce set — the cities
// a new session's invite line is written in ([{zone,name}], plaintext JSON
// beside users.sizes). The session keeps its own copy; this is only the seed.
func (s *server) handleSetAnnounceCities(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		AnnounceCities json.RawMessage `json:"announce_cities"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.AnnounceCities) > 4096 {
		httpError(w, http.StatusBadRequest, xystrings.Default.Auth.Profile.CitiesTooLong())
		return
	}
	err := s.withWriteTx(r.Context(), "set-announce-cities", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update users set announce_cities = ?, updated_at = ? where id = ?`,
			string(req.AnnounceCities), rfc3339(time.Now()), u.UserID)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sessionTitleModes allowlists users.session_title_mode: how a session's derived
// label name is written. "" means the default, "date-title".
var sessionTitleModes = map[string]bool{"": true, "date-title": true, "title": true, "date": true}

// cardTitleModes allowlists the values of users.card_title (see schema v13):
// which field a card's list preview derives its title from. "" means the
// default, "question".
var cardTitleModes = map[string]bool{"": true, "question": true, "answer": true}

// handleSetCardTitle stores whether card previews show the question text or the
// answer (users.card_title, see schema v13). A card's alias, when set, wins over
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

// feedDefaults allowlists the values of users.feed_default (see schema v20):
// which kind of timeline entry an opened card's timeline shows. "" means the
// default, "all".
var feedDefaults = map[string]bool{"": true, "all": true, "comments": true, "edits": true, "meta": true}

// handleSetFeedDefault stores which kind of timeline entry a card opens on
// (users.feed_default, see schema v20).
func (s *server) handleSetFeedDefault(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		FeedDefault string `json:"feed_default"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	mode := strings.TrimSpace(req.FeedDefault)
	if !feedDefaults[mode] {
		httpError(w, http.StatusBadRequest, "bad feed_default")
		return
	}
	err := s.withWriteTx(r.Context(), "set-feed-default", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update users set feed_default = ?, updated_at = ? where id = ?`,
			mode, rfc3339(time.Now()), u.UserID)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// nullStr is an empty telegram field as SQL sees it: absent, not "".
func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
