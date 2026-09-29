package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"pecheny.me/dopecore/authcred"
	"pecheny.me/dopecore/session"
)

// API tokens are month-lived bearer credentials a user mints on /profile (or
// through /api/auth/tokens) for a script or an agent. A token is the user
// (ADR-0021): every route a cookie reaches, a token reaches, except changing
// the password or the username and /admin. Only the sha256 of the raw token is
// stored; the raw token is shown once, when it is made.

const (
	apiTokenBytes    = 32
	APITokenLifetime = 30 * 24 * time.Hour
	APITokenLabelMax = 100
)

// BearerToken returns the raw token of an `Authorization: Bearer …` header, or
// "" when the request carries none.
func BearerToken(r *http.Request) string {
	const prefix = "bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

// lookupAPIToken resolves a raw token to its owner. An unknown, revoked or
// expired token is simply «not authenticated», as a dead cookie is.
func (e *Engine) lookupAPIToken(ctx context.Context, raw string) (session.User, bool) {
	if e.DB == nil {
		return session.User{}, false
	}
	var (
		u        session.User
		id       int64
		expires  string
		revoked  sql.NullString
		lastUsed sql.NullString
		isSystem int
	)
	err := e.DB.QueryRowContext(ctx, `
select t.id, t.user_id, t.expires_at, t.revoked_at, t.last_used_at, u.username, u.telegram_username, u.is_system
from api_tokens t join users u on u.id = t.user_id
where t.token_hash = ?`, authcred.HashSessionToken(raw)).
		Scan(&id, &u.UserID, &expires, &revoked, &lastUsed, &u.Username, &u.Telegram, &isSystem)
	if err != nil || revoked.Valid || isSystem == 1 {
		return session.User{}, false
	}
	now := time.Now().UTC()
	if exp, err := time.Parse(time.RFC3339, expires); err != nil || now.After(exp) {
		return session.User{}, false
	}
	// last_used_at answers «is this token still in use», so once a minute is
	// enough; an agent would otherwise write on every request.
	if seen, err := time.Parse(time.RFC3339, lastUsed.String); !lastUsed.Valid || err != nil || now.Sub(seen) >= time.Minute {
		_, _ = e.DB.ExecContext(ctx, `update api_tokens set last_used_at = ? where id = ?`, now.Format(time.RFC3339), id)
	}
	return u, true
}

// APIToken is one token as its owner sees it: never the secret itself.
type APIToken struct {
	ID         int64   `json:"id"`
	Label      string  `json:"label"`
	CreatedAt  string  `json:"created_at"`
	ExpiresAt  string  `json:"expires_at"`
	RevokedAt  *string `json:"revoked_at"`
	LastUsedAt *string `json:"last_used_at"`
	Active     bool    `json:"active"`
}

// ListAPITokens returns the user's tokens, newest first.
func (e *Engine) ListAPITokens(ctx context.Context, userID int64) ([]APIToken, error) {
	rows, err := e.DB.QueryContext(ctx, `
select id, coalesce(label, ''), created_at, expires_at, revoked_at, last_used_at
from api_tokens where user_id = ? order by id desc`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now().UTC()
	out := []APIToken{}
	for rows.Next() {
		var t APIToken
		var revoked, lastUsed sql.NullString
		if err := rows.Scan(&t.ID, &t.Label, &t.CreatedAt, &t.ExpiresAt, &revoked, &lastUsed); err != nil {
			return nil, err
		}
		if revoked.Valid {
			t.RevokedAt = &revoked.String
		}
		if lastUsed.Valid {
			t.LastUsedAt = &lastUsed.String
		}
		exp, _ := time.Parse(time.RFC3339, t.ExpiresAt)
		t.Active = !revoked.Valid && now.Before(exp)
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreatedAPIToken is what minting returns: the raw token, the only time it is
// ever seen.
type CreatedAPIToken struct {
	ID        int64  `json:"id"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// CreateAPIToken mints a token for the user.
func (e *Engine) CreateAPIToken(ctx context.Context, userID int64, label string) (CreatedAPIToken, error) {
	label = strings.TrimSpace(label)
	if r := []rune(label); len(r) > APITokenLabelMax {
		label = string(r[:APITokenLabelMax])
	}
	buf := make([]byte, apiTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return CreatedAPIToken{}, err
	}
	raw := hex.EncodeToString(buf)
	now := time.Now().UTC()
	expires := now.Add(APITokenLifetime).Format(time.RFC3339)
	var labelValue any
	if label != "" {
		labelValue = label
	}
	res, err := e.WriteExec(ctx, `
insert into api_tokens(user_id, token_hash, label, created_at, expires_at)
values(?, ?, ?, ?, ?)`, userID, authcred.HashSessionToken(raw), labelValue, now.Format(time.RFC3339), expires)
	if err != nil {
		return CreatedAPIToken{}, err
	}
	id, err := res.LastInsertId()
	return CreatedAPIToken{ID: id, Token: raw, ExpiresAt: expires}, err
}

// ErrNoAPIToken is a revoke of a token the user does not own or that is gone.
var ErrNoAPIToken = errors.New("no such token")

// RevokeAPIToken revokes one of the user's live tokens.
func (e *Engine) RevokeAPIToken(ctx context.Context, userID, tokenID int64) error {
	res, err := e.WriteExec(ctx, `
update api_tokens set revoked_at = ? where id = ? and user_id = ? and revoked_at is null`,
		time.Now().UTC().Format(time.RFC3339), tokenID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoAPIToken
	}
	return nil
}

// RevokeAllAPITokensTx revokes every live token of the user. Changing or
// resetting the password calls it: one act the user already knows ends every
// leaked credential.
func RevokeAllAPITokensTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	_, err := tx.ExecContext(ctx, `
update api_tokens set revoked_at = ? where user_id = ? and revoked_at is null`,
		time.Now().UTC().Format(time.RFC3339), userID)
	return err
}
