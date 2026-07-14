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
