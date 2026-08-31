// Package tgbridge single-sources the one write the Telegram bot causes:
// consuming a register code. The two apps drive it through different
// disciplines — xy wraps it in a bounded transaction under its global write
// lock, dope holds its global write mutex across a direct exec — and their
// reply text differs. What must not drift is the SQL, and that is what lives
// here. The visitor-facing side of the same handshake (start, status poll,
// claim) is the state machine in package tglogin.
package tgbridge

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
