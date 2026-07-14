package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"pecheny.me/dopecore/authcred"
)

// API tokens authorize the Trello-compatible API (see trello_compat.go). They
// are month-lived bearer credentials, managed by the user at /profile/tokens.
// The raw token is shown once on creation; only its sha256 hash is stored.

const (
	apiTokenBytes    = 32 // 64 hex chars — Trello-token shaped
	apiTokenLifetime = 30 * 24 * time.Hour
)

func newAPIToken() (string, error) {
	buf := make([]byte, apiTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// ---- response shapes ----

type apiTokenDTO struct {
	ID         int64   `json:"id"`
	Label      *string `json:"label"`
	CreatedAt  string  `json:"created_at"`
	ExpiresAt  string  `json:"expires_at"`
	RevokedAt  *string `json:"revoked_at"`
	LastUsedAt *string `json:"last_used_at"`
	Active     bool    `json:"active"`
}

func (s *server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
select id, label, created_at, expires_at, revoked_at, last_used_at
from api_tokens where user_id = ? order by id desc`, u.UserID)
	if handleErr(w, err) {
		return
	}
	defer rows.Close()
	now := time.Now()
	out := []apiTokenDTO{}
	for rows.Next() {
		var t apiTokenDTO
		var label, revoked, lastUsed sql.NullString
		if err := rows.Scan(&t.ID, &label, &t.CreatedAt, &t.ExpiresAt, &revoked, &lastUsed); handleErr(w, err) {
			return
		}
		if label.Valid {
			t.Label = &label.String
		}
		if revoked.Valid {
			t.RevokedAt = &revoked.String
		}
		if lastUsed.Valid {
			t.LastUsedAt = &lastUsed.String
		}
		expires, _ := time.Parse(time.RFC3339, t.ExpiresAt)
		t.Active = !revoked.Valid && now.Before(expires)
		out = append(out, t)
	}
	writeJSON(w, out)
}

type createTokenRequest struct {
	Label string `json:"label"`
}

type createTokenResponse struct {
	ID        int64  `json:"id"`
	Token     string `json:"token"` // raw token, shown once
	ExpiresAt string `json:"expires_at"`
}

func (s *server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req createTokenRequest
	if !readJSON(w, r, &req) {
		return
	}
	label := strings.TrimSpace(req.Label)
	if len(label) > 100 {
		label = label[:100]
	}
	raw, err := newAPIToken()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "ошибка сервера")
		return
	}
	now := time.Now()
	expiresAt := now.Add(apiTokenLifetime)
	var id int64
	err = s.withWriteTx(r.Context(), "create-token", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
insert into api_tokens(user_id, token_hash, label, created_at, expires_at)
values(?, ?, ?, ?, ?)`, u.UserID, authcred.HashSessionToken(raw), nullStr(label), rfc3339(now), rfc3339(expiresAt))
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if handleErr(w, err) {
		return
	}
	writeJSON(w, createTokenResponse{ID: id, Token: raw, ExpiresAt: rfc3339(expiresAt)})
}

func (s *server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	tokenID, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	err := s.withWriteTx(r.Context(), "revoke-token", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
update api_tokens set revoked_at = ?
where id = ? and user_id = ? and revoked_at is null`,
			rfc3339(time.Now()), tokenID, u.UserID)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
