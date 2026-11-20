package authweb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
	"code.pecheny.me/pecheny/dopesuite/dopecore/tglogin"
)

// users is an app's users table in a map: telegram → account, account → name.
type users struct {
	byTelegram map[int64]int64
	names      map[int64]string
	next       int64
}

func (u *users) ByTelegram(_ context.Context, _ tglogin.Tx, tg int64) (tglogin.Account, bool, error) {
	id, ok := u.byTelegram[tg]
	if !ok {
		return tglogin.Account{}, false, nil
	}
	return tglogin.Account{ID: id, Username: sql.NullString{String: u.names[id], Valid: true}}, true, nil
}

func (u *users) ByUsername(_ context.Context, _ tglogin.Tx, name string) (tglogin.Account, bool, error) {
	for id, n := range u.names {
		if n == name {
			return tglogin.Account{ID: id, Username: sql.NullString{String: n, Valid: true}}, true, nil
		}
	}
	return tglogin.Account{}, false, nil
}

func (u *users) Create(_ context.Context, _ tglogin.Tx, id tglogin.Identity, name string, _ time.Time) (int64, error) {
	u.next++
	u.names[u.next] = name
	u.byTelegram[id.TelegramUserID] = u.next
	return u.next, nil
}

func (u *users) Attach(_ context.Context, _ tglogin.Tx, userID int64, id tglogin.Identity, _ time.Time) error {
	u.byTelegram[id.TelegramUserID] = userID
	return nil
}

type fixture struct {
	t     *testing.T
	db    *sql.DB
	users *users
	web   Web
	page  http.Handler // the app: FinishPending over a page that says who it sees
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv(session.ProdEnvVar, "test")
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
create table telegram_login_codes(id integer primary key, code text not null unique, kind text not null,
  telegram_user_id integer, telegram_username text, telegram_name text,
  created_at text not null, expires_at text not null, consumed_at text);
create table sessions(id integer primary key, user_id integer not null, token_hash text not null unique,
  created_at text not null, expires_at text not null, last_seen_at text not null);`); err != nil {
		t.Fatal(err)
	}
	u := &users{byTelegram: map[int64]int64{}, names: map[int64]string{}}
	web := Web{
		Handshake: tglogin.Handshake{Users: u},
		WriteTx: func(ctx context.Context, _ string, fn func(ctx context.Context, tx Tx) error) error {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if err := fn(ctx, tx); err != nil {
				return err
			}
			return tx.Commit()
		},
		ReadJSON:       func(r *http.Request, v any) error { return json.NewDecoder(r.Body).Decode(v) },
		Refuse:         func(code int, msg string) error { return errors.New(msg) },
		HasBot:         func() bool { return true },
		TelegramStatus: func() string { return TelegramOK },
		BotUsername:    func() string { return "test_bot" },
		ValidUsername:  func(s string) bool { return len(s) >= 3 },
	}
	page := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if session.HasCookie(r) {
			_, _ = w.Write([]byte("logged in"))
			return
		}
		_, _ = w.Write([]byte("anonymous"))
	})
	return &fixture{t: t, db: db, users: u, web: web, page: web.FinishPending(page)}
}

// start is the login page in the first tab: it mints a code and the browser
// keeps the pending cookie it came with.
func (f *fixture) start(cookies ...*http.Cookie) (string, *http.Cookie) {
	f.t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/tg/start", nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	if err := f.web.Start(w, r); err != nil {
		f.t.Fatal(err)
	}
	var res StartResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		f.t.Fatal(err)
	}
	return res.Code, cookieNamed(w, PendingCookieName)
}

// bot is the visitor forwarding the code: tgbridge writes who sent it.
func (f *fixture) bot(code string, tg int64) {
	f.t.Helper()
	if _, err := f.db.Exec(`update telegram_login_codes set telegram_user_id = ?, telegram_username = 'someone', consumed_at = ? where code = ?`,
		tg, time.Now().UTC().Format(time.RFC3339), code); err != nil {
		f.t.Fatal(err)
	}
}

// open is a new tab: a page load carrying whatever cookies the browser has.
func (f *fixture) open(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.page.ServeHTTP(w, r)
	return w
}

func cookieNamed(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// The case this exists for: the login was started in one tab, confirmed in
// Telegram, and the person opens the site in a new tab instead of going back.
func TestANewTabFinishesAConfirmedLogin(t *testing.T) {
	f := newFixture(t)
	f.users.names[1], f.users.byTelegram[777] = "alice", 1

	code, pending := f.start()
	if pending == nil || pending.Value != code || !pending.HttpOnly {
		t.Fatalf("start's pending cookie = %+v, want HttpOnly %q", pending, code)
	}

	// Not confirmed yet: the new tab is an anonymous page load, and the
	// login is still pending.
	w := f.open("/2026/nominate", pending)
	if w.Code != http.StatusOK || w.Body.String() != "anonymous" || cookieNamed(w, PendingCookieName) != nil {
		t.Fatalf("before the bot: %d %q, cookies %v", w.Code, w.Body, w.Result().Cookies())
	}

	f.bot(code, 777)
	w = f.open("/2026/nominate?x=1", pending)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/2026/nominate?x=1" {
		t.Fatalf("after the bot: %d to %q, want 303 back to the same page", w.Code, w.Header().Get("Location"))
	}
	sess := cookieNamed(w, session.CookieName)
	if sess == nil || sess.Value == "" {
		t.Fatal("no session cookie")
	}
	if c := cookieNamed(w, PendingCookieName); c == nil || c.MaxAge >= 0 {
		t.Fatalf("pending cookie not cleared: %+v", c)
	}
	if w = f.open("/2026/nominate?x=1", sess); w.Body.String() != "logged in" {
		t.Fatalf("the redirected load: %q", w.Body)
	}

	// The first tab, still polling, finds its code spent: not_found, which the
	// login page answers by checking whether it is logged in after all.
	r := httptest.NewRequest(http.MethodGet, "/api/auth/tg/status?code="+code, nil)
	rw := httptest.NewRecorder()
	if err := f.web.Status(rw, r); err != nil || !strings.Contains(rw.Body.String(), `"not_found"`) {
		t.Fatalf("old tab's poll: %v %s", err, rw.Body)
	}
}

// A brand-new telegram has a username to pick, which only the login page can
// ask: page loads leave it alone, and the login page in ANY tab resumes the
// login from the cookie and claims it without knowing the code.
func TestANewTelegramResumesOnTheLoginPage(t *testing.T) {
	f := newFixture(t)
	code, pending := f.start()
	f.bot(code, 888)

	if w := f.open("/", pending); w.Code != http.StatusOK || w.Body.String() != "anonymous" {
		t.Fatalf("page load: %d %q", w.Code, w.Body)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/auth/tg/status", nil)
	r.AddCookie(pending)
	w := httptest.NewRecorder()
	if err := f.web.Status(w, r); err != nil {
		t.Fatal(err)
	}
	var st StatusResponse
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if st.Status != tglogin.ChooseUsername || st.Code != code || st.BotUsername != "test_bot" {
		t.Fatalf("resumed status = %+v", st)
	}

	r = httptest.NewRequest(http.MethodPost, "/api/auth/tg/claim", strings.NewReader(`{"username":"newbie"}`))
	r.AddCookie(pending)
	w = httptest.NewRecorder()
	if err := f.web.Claim(w, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Body.String(), `"ready"`) || cookieNamed(w, session.CookieName) == nil {
		t.Fatalf("claim from the cookie: %s", w.Body)
	}
}

// With nothing pending, the login page's question on load is answered plainly.
func TestStatusWithNothingPending(t *testing.T) {
	f := newFixture(t)
	w := httptest.NewRecorder()
	if err := f.web.Status(w, httptest.NewRequest(http.MethodGet, "/api/auth/tg/status", nil)); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(w.Body.String()) != `{"status":"not_found"}` {
		t.Fatalf("body = %s", w.Body)
	}
}

// Somebody logged in who starts the handshake is linking a telegram onto their
// account; a pending cookie there could log them into whoever else owns it.
func TestALoggedInStartLeavesNoPendingLogin(t *testing.T) {
	f := newFixture(t)
	if _, pending := f.start(&http.Cookie{Name: session.CookieName, Value: "tok"}); pending != nil {
		t.Fatalf("pending cookie set for a logged-in start: %+v", pending)
	}
}

// Only page loads finish a login: an API call or an asset passes through.
func TestOnlyPageLoadsFinish(t *testing.T) {
	f := newFixture(t)
	f.users.names[1], f.users.byTelegram[777] = "alice", 1
	code, pending := f.start()
	f.bot(code, 777)

	r := httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
	r.Header.Set("Sec-Fetch-Mode", "no-cors")
	r.AddCookie(pending)
	w := httptest.NewRecorder()
	f.page.ServeHTTP(w, r)
	if w.Code != http.StatusOK || cookieNamed(w, session.CookieName) != nil {
		t.Fatalf("asset load: %d, cookies %v", w.Code, w.Result().Cookies())
	}
}
