package core

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"time"

	"dope/dope/platform/session"
)

// HashSessionToken hashes a raw session token into the value stored in
// sessions.token_hash (sha256, hex-encoded).
func HashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// sessionRefreshInterval is the minimum gap between sessions.last_seen_at
// writes for a given session. Most authenticated requests can be served from
// a single SELECT — only every ~minute do we round-trip another write to
// extend the sliding session lifetime.
const sessionRefreshInterval = time.Minute

// LookupSession resolves the request's session cookie to a session.User. The
// second return is false when there is no valid session.
func (e *Engine) LookupSession(r *http.Request) (session.User, bool) {
	if e.DB == nil {
		return session.User{}, false
	}
	cookie, err := r.Cookie(session.CookieName)
	if err != nil || cookie.Value == "" {
		return session.User{}, false
	}
	hash := HashSessionToken(cookie.Value)

	ctx := r.Context()

	var (
		sessionID  int64
		userID     int64
		expiresAt  string
		lastSeenAt string
		username   sql.NullString
		tgUser     sql.NullString
		isSystem   int
	)
	err = e.DB.QueryRowContext(ctx, `
select s.id, s.user_id, s.expires_at, s.last_seen_at,
       u.username, u.telegram_username, u.is_system
from sessions s
join users u on u.id = s.user_id
where s.token_hash = ?`, hash).Scan(&sessionID, &userID, &expiresAt, &lastSeenAt, &username, &tgUser, &isSystem)
	if err != nil {
		return session.User{}, false
	}
	now := time.Now().UTC()
	expiry, _ := time.Parse(time.RFC3339, expiresAt)
	if !expiry.IsZero() && now.After(expiry) {
		_, _ = e.DB.ExecContext(ctx, `delete from sessions where id = ?`, sessionID)
		return session.User{}, false
	}

	// Only bump last_seen_at if it has drifted enough to be worth a write.
	// Without this, every authenticated request triggers a BEGIN / UPDATE /
	// COMMIT against the sessions table. The expiry-window check still
	// guarantees the sliding session lifetime when something (admin tool,
	// migration, test) shortens expires_at independently of last_seen_at.
	lastSeen, _ := time.Parse(time.RFC3339, lastSeenAt)
	needsRefresh := lastSeen.IsZero() || now.Sub(lastSeen) >= sessionRefreshInterval
	if !needsRefresh && !expiry.IsZero() && expiry.Sub(now) < session.Lifetime-sessionRefreshInterval {
		needsRefresh = true
	}
	if needsRefresh {
		newExpires := now.Add(session.Lifetime).Format(time.RFC3339)
		if _, err := e.DB.ExecContext(ctx, `
update sessions set last_seen_at = ?, expires_at = ? where id = ?`,
			now.Format(time.RFC3339), newExpires, sessionID); err != nil {
			return session.User{}, false
		}
	}

	return session.User{
		UserID:    userID,
		Username:  username,
		Telegram:  tgUser,
		IsSystem:  isSystem == 1,
		SessionID: sessionID,
	}, true
}
