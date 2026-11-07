package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
	"code.pecheny.me/pecheny/dopesuite/dopecore/tglogin"
	xystrings "xy/i18nstrings"
)

// Linking a Telegram to an account that has none: the accounts made by hand
// (adduser, /admin) before the bot existed for them. A board passphrase's copy
// goes to the owner's Telegram (passphrasetg.go), so an account without one
// would set passphrases nobody keeps. While this instance's bot is working,
// every page sends such an account to /telegram first (telegramRequired).

// telegramRequired: the account has no Telegram and this instance can link one
// right now. A bot that is down is no reason to lock anyone out, so an
// instance whose bot is not polling asks nothing.
func (s *server) telegramRequired(ctx context.Context, uid int64) (bool, error) {
	if s.telegramStatus() != tgStatusOK {
		return false, nil
	}
	var linked bool
	err := s.db.QueryRowContext(ctx, `select telegram_user_id is not null from users where id = ?`, uid).Scan(&linked)
	return !linked, err
}

func (xyUsers) ByID(ctx context.Context, tx tglogin.Tx, uid int64) (tglogin.Account, bool, error) {
	var a tglogin.Account
	err := tx.QueryRowContext(ctx, `select id, username, password_hash, telegram_user_id from users where id = ?`, uid).
		Scan(&a.ID, &a.Username, &a.PasswordHash, &a.TelegramUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return a, false, nil
	}
	return a, err == nil, err
}

// handleTgLinkStart mints a code the same way the login page does; only a
// logged-in account may, because the code it gets is one it will link.
func (s *server) handleTgLinkStart(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireCookieUser(w, r); !ok {
		return
	}
	s.handleTgStart(w, r)
}

type tgLinkResponse struct {
	Status   string  `json:"status"`
	Telegram *string `json:"telegram,omitempty"`
}

// handleTgLinkStatus is the login page's status poll with the other ending:
// once the bot has seen the code, its Telegram is attached to the account in
// the session, and no new session is made.
func (s *server) handleTgLinkStatus(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireCookieUser(w, r)
	if !ok {
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		httpError(w, http.StatusBadRequest, "code required")
		return
	}
	var out tglogin.Outcome
	err := s.withWriteTx(r.Context(), "tg-link", func(ctx context.Context, tx *sql.Tx) (err error) {
		out, err = s.handshake().Link(ctx, tx, code, u.UserID, time.Now())
		switch {
		case errors.Is(err, tglogin.ErrTelegramLinked):
			return corei18n.User(xystrings.Default.Auth.Tg.TelegramTaken())
		case errors.Is(err, tglogin.ErrAccountLinked):
			return corei18n.User(xystrings.Default.Auth.Tg.AccountLinked())
		}
		return err
	})
	if handleErr(w, err) {
		return
	}
	writeJSON(w, tgLinkResponse{Status: out.Status, Telegram: out.Telegram})
}
