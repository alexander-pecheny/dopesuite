package route

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"dope/dope/domain/core"

	"pecheny.me/dopecore/authcred"
	"pecheny.me/dopecore/session"
)

// The access matrix: every level × every caller, on a public and a private
// fest. Callers are named by their role on the fest; "anon" has no session,
// "outsider" a session and no role.
func newTable(t *testing.T) (*Table, map[string]string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
create table users(id integer primary key, username text, telegram_username text, is_system integer default 0);
create table sessions(id integer primary key, user_id integer, token_hash text unique, created_at text, expires_at text, last_seen_at text);
create table fests(id integer primary key, slug text unique, is_public integer default 0, created_by integer);
create table fest_organizers(fest_id integer, user_id integer, role text, added_at text);
create table games(id integer primary key, fest_id integer, slug text);
create table fest_teams(id integer primary key, fest_id integer, name text, city text default '', position real, number integer, deleted integer default 0);
create table game_participants(game_id integer, participant_id integer, position integer, number integer default 0);
insert into users(id, username) values (1,'creator'),(2,'admin'),(3,'host'),(4,'outsider');
insert into fests(id, slug, is_public, created_by) values (10,'open',1,1),(20,'closed',0,1);
insert into fest_organizers values (10,2,'admin',''),(10,3,'host',''),(20,2,'admin',''),(20,3,'host','');
insert into games(id, fest_id, slug) values (100,10,'g'),(200,20,'g');
insert into fest_teams(fest_id, name, position, number) values (10,'A',1,1),(20,'B',1,null);`); err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{}
	now := time.Now()
	for uid, name := range map[int64]string{1: "creator", 2: "admin", 3: "host", 4: "outsider"} {
		tok, err := authcred.CreateSession(t.Context(), db, uid, now)
		if err != nil {
			t.Fatal(err)
		}
		tokens[name] = tok
	}
	return New(&core.Engine{DB: db}, DenyAPI), tokens
}

func TestAccessMatrix(t *testing.T) {
	tbl, tokens := newTable(t)
	ok := func(w http.ResponseWriter, r *http.Request, sc Scope) error { return JSON(w, sc.Role) }
	levels := map[string]Access{"public": Public, "session": Session, "publicfest": PublicFest, "read": Read, "member": Member, "editor": Editor, "manager": Manager, "creator": Creator}
	for name, a := range levels {
		tbl.Handle("POST /"+name+"/{fest}/games/{game}", a, ok)
	}
	tbl.Handle("POST /numbered/{fest}/games/{game}", Editor.Numbered(), ok)

	const o = 200
	type row struct {
		level, fest                          string
		anon, outsider, host, admin, creator int
	}
	rows := []row{
		{"public", "open", o, o, o, o, o},
		{"public", "closed", o, o, o, o, o},
		{"session", "open", 401, o, o, o, o},
		{"publicfest", "open", o, o, o, o, o},
		{"publicfest", "closed", 404, 404, 404, 404, 404},
		{"read", "open", o, o, o, o, o},
		{"read", "closed", 404, 404, o, o, o},
		{"member", "open", 401, 403, o, o, o},
		{"member", "closed", 401, 403, o, o, o},
		{"editor", "closed", 401, 403, o, o, o},
		{"manager", "closed", 401, 403, 403, o, o},
		{"creator", "closed", 401, 403, 403, 403, o},
		{"numbered", "open", 401, 403, o, o, o},
		{"numbered", "closed", 401, 403, 409, 409, 409},
	}
	for _, rw := range rows {
		for caller, want := range map[string]int{"anon": rw.anon, "outsider": rw.outsider, "host": rw.host, "admin": rw.admin, "creator": rw.creator} {
			req := httptest.NewRequest("POST", "/"+rw.level+"/"+rw.fest+"/games/g", nil)
			if caller != "anon" {
				req.AddCookie(&http.Cookie{Name: session.CookieName, Value: tokens[caller]})
			}
			rec := httptest.NewRecorder()
			tbl.Mux.ServeHTTP(rec, req)
			if rec.Code != want {
				t.Errorf("%s on %s as %s: %d, want %d (%s)", rw.level, rw.fest, caller, rec.Code, want, rec.Body.String())
			}
		}
	}
}

func TestPathResolutionAndErrors(t *testing.T) {
	tbl, _ := newTable(t)
	tbl.Handle("GET /f/{fest}/g/{game}", Public, func(w http.ResponseWriter, r *http.Request, sc Scope) error {
		return JSON(w, []int64{sc.FestID, sc.GameID})
	})
	tbl.Handle("GET /boom/{fest}", Public, func(http.ResponseWriter, *http.Request, Scope) error { return BadRequest("nope") })
	tbl.Handle("GET /gone/{fest}", Public, func(http.ResponseWriter, *http.Request, Scope) error { return sql.ErrNoRows })
	cases := map[string]struct {
		code int
		body string
	}{
		"/f/open/g/g":   {200, "[10,100]"},
		"/f/10/g/100":   {200, "[10,100]"},
		"/f/open/g/200": {404, ""},
		"/f/nope/g/g":   {404, ""},
		"/boom/open":    {400, "nope\n"},
		"/gone/open":    {404, ""},
	}
	for path, want := range cases {
		rec := httptest.NewRecorder()
		tbl.Mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != want.code || (want.body != "" && rec.Body.String() != want.body) {
			t.Errorf("%s: %d %q, want %d %q", path, rec.Code, rec.Body.String(), want.code, want.body)
		}
	}
	rec := httptest.NewRecorder()
	tbl.Mux.ServeHTTP(rec, httptest.NewRequest("POST", "/f/open/g/g", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("wrong method: %d", rec.Code)
	}
}

func TestSameOriginOnUnsafeMethods(t *testing.T) {
	tbl, _ := newTable(t)
	tbl.Handle("POST /x", Public, func(w http.ResponseWriter, r *http.Request, sc Scope) error { return nil })
	req := httptest.NewRequest("POST", "/x", nil)
	req.Host = "dope.test"
	req.Header.Set("Origin", "https://evil.test")
	rec := httptest.NewRecorder()
	tbl.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST: %d", rec.Code)
	}
	t.Setenv(TrustedOriginHostsEnv, "https://evil.test")
	rec = httptest.NewRecorder()
	tbl.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("trusted origin: %d", rec.Code)
	}
}

func TestGamePagePath(t *testing.T) {
	cases := []struct {
		parts []string
		host  bool
		want  bool
	}{
		{[]string{"game", "g"}, false, true},
		{[]string{"game", "g", "venues"}, false, true},
		{[]string{"game", "g", "seed-import"}, false, false},
		{[]string{"game", "g", "seed-import"}, true, true},
		{[]string{"game", "g", "stage", "s1"}, false, true},
		{[]string{"game", "g", "stage"}, false, false},
		{[]string{"game", ""}, true, false},
		{[]string{"team", "g"}, true, false},
	}
	for _, c := range cases {
		if got := GamePagePath(c.parts, c.host); got != c.want {
			t.Errorf("%v host=%v: %v", c.parts, c.host, got)
		}
	}
}
