package spliffserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pecheny.me/dopecore/authcred"
	"pecheny.me/dopecore/blobstore"
	"pecheny.me/dopecore/session"

	"spliff/spliff/domain/rates"
	"spliff/spliff/storage/store"
)

// TestServer is the one exported seam the HTTP tests go through: a real server
// over an in-memory database, a real route table, no bot and no network. It is
// dope's testapi.go shape — the tests live in their own package and reach the
// server only through what this file exposes.
type TestServer struct {
	t   *testing.T
	s   *server
	mux *http.ServeMux
}

// NewTestServer builds a server on a fresh database in dir, with the assets and
// pages wired up exactly as Main wires them.
func NewTestServer(t *testing.T, dir string) *TestServer {
	t.Helper()
	db, err := openDB(dir + "/spliff.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	blobs, err := blobstore.New(dir + "/blobs")
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}
	s := &server{db: db, blobs: blobs}
	// No bot: nothing polls, and the three DMs are no-ops. No HTTP client
	// either — the rate service below never fetches, because the fixture table
	// is written straight into the database.
	s.rates = newRateService(s, nil)
	s.assets, s.pages = newAssets()
	if err := s.pages.Warm(pagePaths...); err != nil {
		t.Fatalf("warm pages: %v", err)
	}
	return &TestServer{t: t, s: s, mux: routes(s)}
}

// Serve runs one request through the whole table.
func (ts *TestServer) Serve(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	ts.mux.ServeHTTP(w, r)
	return w
}

// AddUser mints a password account and answers its id.
func (ts *TestServer) AddUser(username, passwordHash string) int64 {
	ts.t.Helper()
	now := rfc3339(time.Now())
	res, err := ts.s.db.Exec(`
insert into users(username, password_hash, created_at, updated_at) values(?, ?, ?, ?)`,
		username, passwordHash, now, now)
	if err != nil {
		ts.t.Fatalf("add user %s: %v", username, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		ts.t.Fatalf("add user %s: %v", username, err)
	}
	return id
}

// Login mints a session for a user and answers the cookie to send with it.
func (ts *TestServer) Login(userID int64) *http.Cookie {
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

// SeedRates writes a Rate table for a day, so no test ever opens a socket.
func (ts *TestServer) SeedRates(day string, table map[string]string) {
	ts.t.Helper()
	err := ts.s.withWriteTx(ts.t.Context(), "test-rates", func(ctx context.Context, tx *sql.Tx) error {
		return store.SaveRateTable(ctx, tx, day, "test fixture", table, rfc3339(time.Now()))
	})
	if err != nil {
		ts.t.Fatalf("seed rates: %v", err)
	}
}

// Today is the day SeedRates should normally be called with: the one the rate
// service would go looking for.
func Today() string { return time.Now().UTC().Format(rates.DayFormat) }

// DB is the handle a test reaches for when it wants to assert on a row rather
// than on a response.
func (ts *TestServer) DB() *sql.DB { return ts.s.db }

// OpenDB opens a database with the migrations applied, for the schema pin.
func OpenDB(path string) (*sql.DB, error) { return openDB(path) }

// Client is one logged-in person driving the API the way a browser does: the
// session cookie on every request, and the Origin header a same-origin fetch
// carries, so the write-side guard is exercised rather than bypassed.
type Client struct {
	ts     *TestServer
	cookie *http.Cookie
	UserID int64
}

// As mints a session for a user and answers a Client that speaks as them.
func (ts *TestServer) As(userID int64) *Client {
	return &Client{ts: ts, cookie: ts.Login(userID), UserID: userID}
}

// Anonymous is a caller with no session at all — an invitee who followed a link
// while logged out.
func (ts *TestServer) Anonymous() *Client { return &Client{ts: ts} }

// Do sends one request and answers the recorder. body nil sends none; anything
// else is marshalled as JSON.
func (c *Client) Do(method, target string, body any) *httptest.ResponseRecorder {
	c.ts.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.ts.t.Fatalf("marshal %s %s: %v", method, target, err)
		}
		reader = bytes.NewReader(raw)
	}
	r := httptest.NewRequest(method, "http://spliff.test"+target, reader)
	r.Header.Set("Origin", "http://spliff.test")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != nil {
		r.AddCookie(c.cookie)
	}
	return c.ts.Serve(r)
}

// JSON sends a request, insists on a 2xx and decodes the answer into out.
func (c *Client) JSON(method, target string, body, out any) {
	c.ts.t.Helper()
	w := c.Do(method, target, body)
	if w.Code < 200 || w.Code > 299 {
		c.ts.t.Fatalf("%s %s = %d: %s", method, target, w.Code, w.Body.String())
	}
	if out == nil || w.Body.Len() == 0 {
		return
	}
	if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
		c.ts.t.Fatalf("%s %s: decode: %v (%s)", method, target, err, w.Body.String())
	}
}

// Upload posts one file as the multipart form the Photo endpoint reads.
func (c *Client) Upload(target, field, filename string, content []byte) *httptest.ResponseRecorder {
	c.ts.t.Helper()
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	part, err := form.CreateFormFile(field, filename)
	if err != nil {
		c.ts.t.Fatalf("multipart: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		c.ts.t.Fatalf("multipart: %v", err)
	}
	if err := form.Close(); err != nil {
		c.ts.t.Fatalf("multipart: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://spliff.test"+target, &buf)
	r.Header.Set("Origin", "http://spliff.test")
	r.Header.Set("Content-Type", form.FormDataContentType())
	if c.cookie != nil {
		r.AddCookie(c.cookie)
	}
	return c.ts.Serve(r)
}
