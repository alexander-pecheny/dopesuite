package authcred

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"pecheny.me/dopecore/session"
)

func TestSessionsLookupStatesAndSlide(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
create table users(id integer primary key, username text, telegram_username text, is_system integer default 0);
create table sessions(id integer primary key, user_id integer, token_hash text unique, created_at text, expires_at text, last_seen_at text);
insert into users(id, username, is_system) values (1, 'ann', 0), (2, 'sys', 1);`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	store := Sessions{
		UserColumns: "u.username, u.is_system",
		UserDest: func(u *session.User) ([]any, func()) {
			var sys int
			return []any{&u.Username, &sys}, func() { u.IsSystem = sys == 1 }
		},
	}
	tok, err := CreateSession(ctx, db, 2, now)
	if err != nil {
		t.Fatal(err)
	}

	if _, st, _ := store.Lookup(ctx, db, "nope", now); st != NoSession {
		t.Fatalf("unknown token: %v", st)
	}
	u, st, slide := store.Lookup(ctx, db, tok, now.Add(10*time.Second))
	if st != Live || slide || u.UserID != 2 || u.Username.String != "sys" || !u.IsSystem {
		t.Fatalf("fresh: %+v %v slide=%v", u, st, slide)
	}
	if _, _, slide := store.Lookup(ctx, db, tok, now.Add(2*time.Minute)); !slide {
		t.Fatal("a minute later the slide is due")
	}
	if err := SlideSession(ctx, db, u.SessionID, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, slide := store.Lookup(ctx, db, tok, now.Add(2*time.Minute+10*time.Second)); slide {
		t.Fatal("just slid: not due")
	}
	u, st, _ = store.Lookup(ctx, db, tok, now.Add(40*24*time.Hour))
	if st != Expired || u.SessionID == 0 {
		t.Fatalf("past the lifetime: %v %+v", st, u)
	}
	if err := DeleteSession(ctx, db, u.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, st, _ := store.Lookup(ctx, db, tok, now); st != NoSession {
		t.Fatalf("after delete: %v", st)
	}
}
