package spliffserver

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"pecheny.me/dopecore/authcred"
	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopecore/tglogin"

	"spliff/spliff/web/route"

	spliffstrings "spliff/i18nstrings"
)

// profile.go is everything the /profile page does to the account behind the
// session. Neither act names a user: the session does, and both routes ask
// nothing but LoggedIn.
//
// An account is reached by a password or by a Telegram, and most accounts here
// have only the one they were made with — `adduser` mints a password and no
// Telegram, the handshake the other way round. These are the two steps that
// add the missing half.

// handleSetPassword sets the account's password, and is also its kill switch:
// a new password logs out every other session of the account (xy does the same,
// ADR-0015), so a browser left signed in somewhere is one act away from being
// forgotten. The current password is proven when there is one; an account made
// through Telegram has none, and sets its first without proving a previous.
func (s *server) handleSetPassword(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	str := spliffstrings.Default
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if len(req.NewPassword) < authcred.PasswordMinLen || len(req.NewPassword) > authcred.PasswordMaxLen {
		return corei18n.User(str.Profile.Password.Length())
	}
	hash, err := authcred.HashPassword(req.NewPassword)
	if err != nil {
		return err
	}
	now := rfc3339(time.Now())
	err = s.withWriteTx(r.Context(), "set-password", func(ctx context.Context, tx *sql.Tx) error {
		var current, salt sql.NullString
		if err := tx.QueryRowContext(ctx,
			`select password_hash, password_salt from users where id = ?`, sc.User.UserID).
			Scan(&current, &salt); err != nil {
			return err
		}
		if current.Valid && current.String != "" {
			ok, _, err := authcred.VerifyPasswordUpgrading(current.String, salt.String, req.CurrentPassword)
			if err != nil {
				return err
			}
			if !ok {
				return corei18n.User(str.Profile.Password.CurrentWrong())
			}
		}
		// The salt goes with the old hash: it belongs to the legacy scheme, and
		// what is written here is always bcrypt.
		if _, err := tx.ExecContext(ctx,
			`update users set password_hash = ?, password_salt = null, updated_at = ? where id = ?`,
			hash, now, sc.User.UserID); err != nil {
			return err
		}
		// authcred forgets one session at a time (DeleteSession), which is what
		// logging out is; "every other one" is a single statement and has this
		// one caller, so it stays here rather than becoming a shared helper.
		_, err := tx.ExecContext(ctx, `delete from sessions where user_id = ? and id <> ?`,
			sc.User.UserID, sc.User.SessionID)
		return err
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type tgLinkResponse struct {
	Status   string  `json:"status"`
	Telegram *string `json:"telegram,omitempty"`
}

// handleTgLinkStatus polls a code minted for linking. It is the login page's
// status poll with the other ending: tglogin.Link settles the Telegram on the
// account already in the session instead of minting one, so the answer is
// "pending" until the bot has written back and then "linked" with the handle.
// The refusals are the two an account can meet: the Telegram is somebody
// else's, or this account already has one.
func (s *server) handleTgLinkStatus(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	str := spliffstrings.Default
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		return corei18n.User(str.Auth.Tg.CodeMissing())
	}
	var out tglogin.Outcome
	err := s.withWriteTx(r.Context(), "tg-link", func(ctx context.Context, tx *sql.Tx) error {
		var err error
		out, err = s.handshake().Link(ctx, tx, code, sc.User.UserID, time.Now())
		switch {
		case errors.Is(err, tglogin.ErrTelegramLinked):
			return corei18n.User(str.Auth.Tg.TelegramTaken())
		case errors.Is(err, tglogin.ErrAccountLinked):
			return corei18n.User(str.Profile.Telegram.AccountTaken())
		}
		return err
	})
	if err != nil {
		return err
	}
	return writeJSON(w, tgLinkResponse{Status: out.Status, Telegram: out.Telegram})
}
