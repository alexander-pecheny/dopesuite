package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os/user"
	"strings"
	"time"

	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
)

// Local mode is xy inside the desktop app: one person on one computer. There is
// no login, because whoever runs the app is the only user; there is no second
// person, so members, invites and tokens are gone; and boards are
// stored as plaintext, because the disk they sit on is the user's own.

// plainKDFParams is the kdf_params of a plaintext board, whose salt, wrapped key
// and verify token are all empty. Only a local server creates one, and a local
// server creates nothing else.
const plainKDFParams = `{"kdf":"none"}`

// isPlainKDF reports whether kdf_params names no KDF at all.
func isPlainKDF(params string) bool {
	var p struct {
		KDF string `json:"kdf"`
	}
	return json.Unmarshal([]byte(params), &p) == nil && p.KDF == "none"
}

// validKeymeta is the rule a board's key material passes on this server: a
// local board has none, a server board has all of it.
func (s *server) validKeymeta(params string, salt, wrapped, verify []byte) bool {
	if s.cfg.Local {
		return isPlainKDF(params) && len(salt) == 0 && len(wrapped) == 0 && len(verify) == 0
	}
	return params != "" && !isPlainKDF(params) && len(salt) > 0 && len(wrapped) > 0 && len(verify) > 0
}

// localOff is what a local server leaves out, as the frontend reads it from
// /api/auth/me. Each name is a feature, not a route.
var localOff = []string{
	"login",          // login, logout, password, the login page
	"telegram_login", // the handshake and the bot
	"tokens",         // API tokens and the Trello-compatible API they open
	"members",        // members, collaborators, leaving a board
	"invites",        // invite links and join requests
	"passphrase",     // a board's passphrase, its change and its monthly check
	"offline",        // the outbox and the IndexedDB mirror
	"activity",       // the 🔔: nobody else writes here
	"admin",
}

// instanceCaps is the capability set /api/auth/me carries on a local server.
type instanceCaps struct {
	Mode string   `json:"mode"`
	Off  []string `json:"off"`
}

func (s *server) instance() *instanceCaps {
	if !s.cfg.Local {
		return nil
	}
	return &instanceCaps{Mode: "local", Off: localOff}
}

// serverOnly is a route that needs a server or a second person: a local server
// answers it 404.
func (s *server) serverOnly(h http.HandlerFunc) http.HandlerFunc {
	if s.cfg.Local {
		return http.NotFound
	}
	return h
}

// localUsername is the computer's account name when it makes a valid xy
// username, and "local" otherwise.
func localUsername() string {
	const fallback = "local"
	u, err := user.Current()
	if err != nil {
		return fallback
	}
	// Windows answers DOMAIN\name.
	name := u.Username[strings.LastIndex(u.Username, `\`)+1:]
	if !validNewUsername(name) {
		return fallback
	}
	return name
}

// ensureLocalUser returns the local server's one user, creating it the first
// time the database is opened.
func ensureLocalUser(ctx context.Context, s *server) (session.User, error) {
	var u session.User
	err := s.withWriteTx(ctx, "local-user", func(ctx context.Context, tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `select id, username from users order by id limit 1`).Scan(&u.UserID, &u.Username)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now := rfc3339(time.Now())
		name := localUsername()
		res, err := tx.ExecContext(ctx, `insert into users(username, created_at, updated_at) values(?, ?, ?)`, name, now, now)
		if err != nil {
			return err
		}
		u.UserID, err = res.LastInsertId()
		u.Username = sql.NullString{String: name, Valid: true}
		return err
	})
	return u, err
}
