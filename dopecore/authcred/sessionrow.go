package authcred

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"pecheny.me/dopecore/session"
	"pecheny.me/dopecore/sqlitex"
)

// Execer is the write side of a session store: a *sql.Tx, or any type that
// routes the write through the app's own write discipline.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// CreateSession inserts a session row and returns the raw token — only the hash
// is stored. A token collision is astronomically unlikely but cheap to retry;
// any other error is real and returned at once.
func CreateSession(ctx context.Context, ex Execer, userID int64, now time.Time) (string, error) {
	for range 3 {
		token, err := NewSessionToken()
		if err != nil {
			return "", err
		}
		_, err = ex.ExecContext(ctx, `
insert into sessions(user_id, token_hash, created_at, expires_at, last_seen_at)
values(?, ?, ?, ?, ?)`,
			userID, HashSessionToken(token), rfc3339(now), rfc3339(now.Add(session.Lifetime)), rfc3339(now))
		if err == nil {
			return token, nil
		}
		if !sqlitex.IsUniqueViolation(err) {
			return "", err
		}
	}
	return "", errors.New("could not allocate session token")
}

// SessionRefreshInterval is the minimum gap between sessions.last_seen_at
// writes for a given session. Most authenticated requests are served from a
// single SELECT; only every ~minute do we round-trip a write to extend the
// sliding lifetime.
const SessionRefreshInterval = time.Minute

// NeedsRefresh reports whether a session's sliding expiry should be extended.
// It says yes once last_seen_at has drifted by SessionRefreshInterval — and
// also whenever expires_at has drifted in from the full lifetime, which keeps
// the sliding window correct if something (an admin tool, a migration, a test)
// shortened expires_at independently of last_seen_at.
func NeedsRefresh(lastSeen, expiry, now time.Time) bool {
	if lastSeen.IsZero() || now.Sub(lastSeen) >= SessionRefreshInterval {
		return true
	}
	return !expiry.IsZero() && expiry.Sub(now) < session.Lifetime-SessionRefreshInterval
}

// Queryer is the read side of a session store.
type Queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// SessionState is what a token resolves to.
type SessionState int

const (
	NoSession SessionState = iota // unknown token
	Expired                       // a row past its expiry — the caller deletes it
	Live
)

// Sessions resolves cookies over an app's users table. The session columns are
// the same in both apps; the user columns differ (dope reads is_system), so
// the app names the ones the join selects — prefixed "u." — and says where
// they scan: UserDest answers the destinations in column order and a step to
// run once scanned (an int column into a bool), or nil.
type Sessions struct {
	UserColumns string
	UserDest    func(u *session.User) (dest []any, done func())
}

// Lookup resolves a raw session token in one statement: the user, the state,
// and whether the sliding expiry is due (NeedsRefresh) — the caller slides it
// with SlideSession under its own write discipline, as it deletes an Expired
// row. A scan error reads as NoSession, like a missing row.
func (s Sessions) Lookup(ctx context.Context, q Queryer, token string, now time.Time) (u session.User, state SessionState, slide bool) {
	var expiresAt, lastSeenAt string
	row := q.QueryRowContext(ctx, `
select s.id, s.user_id, s.expires_at, s.last_seen_at, `+s.UserColumns+`
from sessions s join users u on u.id = s.user_id
where s.token_hash = ?`, HashSessionToken(token))
	userDest, done := s.UserDest(&u)
	if err := row.Scan(append([]any{&u.SessionID, &u.UserID, &expiresAt, &lastSeenAt}, userDest...)...); err != nil {
		return session.User{}, NoSession, false
	}
	if done != nil {
		done()
	}
	expiry, _ := time.Parse(time.RFC3339, expiresAt)
	if !expiry.IsZero() && now.After(expiry) {
		return u, Expired, false
	}
	lastSeen, _ := time.Parse(time.RFC3339, lastSeenAt)
	return u, Live, NeedsRefresh(lastSeen, expiry, now)
}

// SlideSession extends a live session by the full Lifetime from now.
func SlideSession(ctx context.Context, ex Execer, sessionID int64, now time.Time) error {
	_, err := ex.ExecContext(ctx, `update sessions set last_seen_at = ?, expires_at = ? where id = ?`,
		rfc3339(now), rfc3339(now.Add(session.Lifetime)), sessionID)
	return err
}

// DeleteSession forgets one session (an expired one, a logout).
func DeleteSession(ctx context.Context, ex Execer, sessionID int64) error {
	_, err := ex.ExecContext(ctx, `delete from sessions where id = ?`, sessionID)
	return err
}
