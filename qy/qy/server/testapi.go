package qyserver

import (
	"bytes"
	"context"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"code.pecheny.me/pecheny/dopesuite/dopecore/authcred"
	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
	"code.pecheny.me/pecheny/dopesuite/dopecore/sqlitex/sqlitextest"
	"code.pecheny.me/pecheny/dopesuite/dopecore/tgbot"

	"qy/qy/platform/buff"
	"qy/qy/platform/gq"
	"qy/qy/platform/rating"
	"qy/qy/platform/telegram"
	"qy/qy/storage/store"
)

// TestServer is the one exported seam the HTTP tests go through: a real server
// over a test database, a real route table, no bot and no network. Time,
// gotquestions and the language model are the test's to set.
type TestServer struct {
	t   *testing.T
	s   *server
	mux *http.ServeMux
}

func NewTestServer(t *testing.T, dir string) *TestServer {
	t.Helper()
	db, err := openTestDB(dir + "/qy.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	s := &server{db: db, gq: gq.Unavailable{}, buff: buff.None{}, rating: noRating{}, tg: telegram.Unavailable{}, wake: make(chan struct{}, 1), now: time.Now}
	s.assets, s.pages = newAssets()
	if err := s.pages.Warm(pagePaths...); err != nil {
		t.Fatalf("warm pages: %v", err)
	}
	return &TestServer{t: t, s: s, mux: routes(s)}
}

// noRating is rating.chgk.info that never answers, so no test opens a socket.
type noRating struct{}

func (noRating) Tournament(context.Context, int64) (rating.Tournament, error) {
	return rating.Tournament{}, context.Canceled
}

func (ts *TestServer) SetNow(t time.Time)              { ts.s.now = func() time.Time { return t } }
func (ts *TestServer) SetGotQuestions(src gq.Source)   { ts.s.gq = src }
func (ts *TestServer) SetTelegram(src telegram.Source) { ts.s.tg = src }
func (ts *TestServer) SetRating(src rating.Source)     { ts.s.rating = src }
func (ts *TestServer) SetBuff(names buff.Names)        { ts.s.buff = names }

// Buff is a stand-in mirror of the rating site that knows the names given.
type Buff map[int64]string

func (b Buff) Players(context.Context, []int64) map[int64]string { return b }

// BotMessage hands the bot one message, as a private chat's update, and
// returns what the bot kept of it: its reply, if it was a forwarded post.
func (ts *TestServer) BotMessage(m tgbot.Message) (string, bool) {
	return ts.s.keepForward(context.Background(), &m)
}

// Model is a stand-in language model that answers every prompt with Reply.
type Model struct{ Reply string }

func (m *Model) Complete(context.Context, string, string) (string, error) { return m.Reply, nil }

func (ts *TestServer) SetModel(m *Model) { ts.s.llm = m }

// ReviewPending runs the background pass until no question waits for it.
func (ts *TestServer) ReviewPending() {
	for {
		more, err := ts.s.reviewOne(ts.t.Context())
		if err != nil {
			ts.t.Fatalf("review pass: %v", err)
		}
		if !more {
			return
		}
	}
}

func (ts *TestServer) Serve(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	ts.mux.ServeHTTP(w, r)
	return w
}

// AddUser makes an account with a telegram handle, the way the handshake
// would, and answers its id.
func (ts *TestServer) AddUser(handle string) int64 {
	ts.t.Helper()
	now := rfc3339(time.Now())
	res, err := ts.s.db.Exec(`
insert into users(telegram_user_id, telegram_username, username, created_at, updated_at) values(
  (select coalesce(max(telegram_user_id), 1000) + 1 from users), ?, ?, ?, ?)`, handle, handle, now, now)
	if err != nil {
		ts.t.Fatalf("add user %s: %v", handle, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (ts *TestServer) Exec(query string, args ...any) {
	ts.t.Helper()
	if _, err := ts.s.db.Exec(query, args...); err != nil {
		ts.t.Fatalf("%s: %v", query, err)
	}
}

func (ts *TestServer) DB() *sql.DB { return ts.s.db }

func (ts *TestServer) ImportArchive(root string) (int, int) {
	ts.t.Helper()
	pages, files, err := ts.s.importArchive(ts.t.Context(), root)
	if err != nil {
		ts.t.Fatalf("import: %v", err)
	}
	return pages, files
}

func (ts *TestServer) login(userID int64) *http.Cookie {
	ts.t.Helper()
	var token string
	err := ts.s.withWriteTx(ts.t.Context(), "test-login", func(ctx context.Context, tx *sql.Tx) error {
		var err error
		token, err = authcred.CreateSession(ctx, tx, userID, time.Now())
		return err
	})
	if err != nil {
		ts.t.Fatalf("login %d: %v", userID, err)
	}
	return &http.Cookie{Name: session.CookieName, Value: token}
}

// OpenDB opens a database with the migrations applied, for the schema pin.
func OpenDB(path string) (*sql.DB, error) { return openTestDB(path) }

// openTestDB opens without the fsyncs (sqlitextest); production's openDB
// keeps synchronous(FULL), and durable_test.go holds it to that.
func openTestDB(path string) (*sql.DB, error) { return sqlitextest.Open(path, store.Migrate) }

// Client is one person driving the site the way a browser does: the session
// cookie on every request and the Origin a same-origin form post carries.
type Client struct {
	ts     *TestServer
	cookie *http.Cookie
	UserID int64
}

func (ts *TestServer) As(userID int64) *Client {
	return &Client{ts: ts, cookie: ts.login(userID), UserID: userID}
}

func (ts *TestServer) Anonymous() *Client { return &Client{ts: ts} }

func (c *Client) request(method, target string, body string, contentType string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://qy.test"+target, strings.NewReader(body))
	r.Header.Set("Origin", "http://qy.test")
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if c.cookie != nil {
		r.AddCookie(c.cookie)
	}
	return c.ts.Serve(r)
}

func (c *Client) Get(target string) *httptest.ResponseRecorder {
	return c.request(http.MethodGet, target, "", "")
}

// Post sends a form the way a page's form does.
func (c *Client) Post(target string, form url.Values) *httptest.ResponseRecorder {
	return c.request(http.MethodPost, target, form.Encode(), "application/x-www-form-urlencoded")
}

// File is one file a multipart post attaches.
type File struct {
	Field, Name string
	Data        []byte
}

// PostMultipart sends a form with files, the way a form with a file field does.
func (c *Client) PostMultipart(target string, form url.Values, files ...File) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, vs := range form {
		for _, v := range vs {
			_ = mw.WriteField(k, v)
		}
	}
	for _, f := range files {
		fw, _ := mw.CreateFormFile(f.Field, f.Name)
		_, _ = fw.Write(f.Data)
	}
	_ = mw.Close()
	return c.request(http.MethodPost, target, buf.String(), mw.FormDataContentType())
}

// PostJSON sends a JSON body, the way a page's script does.
func (c *Client) PostJSON(target, body string) *httptest.ResponseRecorder {
	return c.request(http.MethodPost, target, body, "application/json")
}
