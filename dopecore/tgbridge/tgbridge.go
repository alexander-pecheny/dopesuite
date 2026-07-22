// Package tgbridge single-sources the server side of the Telegram
// login/registration handshake: the wire protocol the bot speaks, the
// shared-secret gate, and the two SQL statements that back it.
//
// It deliberately does NOT own the handlers. The two apps drive these writes
// through genuinely different disciplines — xy wraps every write in a bounded
// transaction under its global write lock, dope holds its global write mutex
// across a direct DB exec — and their user-facing reply text differs. Wrapping
// that in a shared handler would cost more adapter than it saves. What must not
// drift is the protocol and the SQL, and that is what lives here.
//
// Background: the bot used to open the database directly, which made it a second
// long-lived writer on the live file. It now holds no database handle and calls
// these endpoints instead; the server stays the sole owner of the DB. The
// shared secret is what keeps the code-issuing endpoints closed to everyone else.
package tgbridge

import (
	"crypto/subtle"
	"net/http"
)

// RegisterRequest is the bot's POST body when a user sends it a register code.
type RegisterRequest struct {
	Code             string `json:"code"`
	TelegramUserID   int64  `json:"telegram_user_id"`
	TelegramUsername string `json:"telegram_username"`
	TelegramName     string `json:"telegram_name"`
}

// LoginRequest is the bot's POST body when a registered user asks for a login code.
type LoginRequest struct {
	TelegramUserID   int64  `json:"telegram_user_id"`
	TelegramUsername string `json:"telegram_username"`
	TelegramName     string `json:"telegram_name"`
}

// Response is what the server sends back: the text the bot echoes to the user.
type Response struct {
	Message string `json:"message"`
}

// SecretOK checks the X-Bot-Secret header in constant time. configured is false
// when no secret is set, which must disable the bridge outright (503) rather
// than leave the code-issuing endpoints open to unauthenticated callers.
func SecretOK(r *http.Request, secret string) (ok, configured bool) {
	if secret == "" {
		return false, false
	}
	got := r.Header.Get("X-Bot-Secret")
	return subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1, true
}

// ConsumeRegisterSQL marks a pending 'register' code as consumed by the telegram
// account that sent it. Params: telegram_user_id, telegram_username, telegram_name,
// now, code, now. It affects one row exactly when the code exists, is a register
// code, is unused, and has not expired — so RowsAffected() == 1 is the success signal.
const ConsumeRegisterSQL = `
update telegram_login_codes
set telegram_user_id = ?, telegram_username = ?, telegram_name = ?, consumed_at = ?
where code = ? and kind = 'register' and consumed_at is null and expires_at > ?`

// LooksLikeRegisterCode is a cheap shape check (base32 alphabet, sane length) so
// an obviously-bogus message never reaches the database.
func LooksLikeRegisterCode(s string) bool {
	if len(s) < 4 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z') && !(r >= '2' && r <= '7') {
			return false
		}
	}
	return true
}
