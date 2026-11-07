package server

import (
	"context"
	"database/sql"
	"html"
	"log"
	"net/http"
	"time"

	xystrings "xy/i18nstrings"
)

// The owner's copy of a Board Passphrase, sent by the bot to their own
// Telegram chat. The browser posts the words once, right after it set them
// (create, import, change) or after the owner typed them at the Passphrase
// Check. The server sends them on and forgets them: they are never written to
// the database or to a log. What the database keeps is passphrase_sent_at, the
// moment the CURRENT words were sent, which a passphrase change clears.
//
// This is a recovery path for an owner who saved the words nowhere and then
// lost the one device that held the key. It moves no trust: the server already
// serves the code that sees every passphrase. What it keeps is the property
// that matters, that nothing in xy.db or its backups opens a board.

// passphraseCopy reports whether a passphrase this user sets has somewhere to
// go: this instance runs a bot and the account is linked to Telegram.
func (s *server) passphraseCopy(ctx context.Context, uid int64) (bool, error) {
	if s.bot == nil {
		return false, nil
	}
	var linked bool
	err := s.db.QueryRowContext(ctx, `select telegram_user_id is not null from users where id = ?`, uid).Scan(&linked)
	return linked, err
}

type sendPassphraseRequest struct {
	Passphrase string `json:"passphrase"`
	// UnsentOnly is the Passphrase Check's typed path: the words are proven
	// again every month, and a copy already sent need not be sent each time.
	UnsentOnly bool `json:"unsent_only"`
}

type sendPassphraseResponse struct {
	// Sent: a message went out on this call.
	Sent bool `json:"sent"`
	// Kept: the owner's Telegram holds the current words, sent now or earlier.
	Kept bool `json:"kept"`
}

func (s *server) handleSendPassphrase(w http.ResponseWriter, r *http.Request) {
	uid, bid, role, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	if role != "owner" {
		httpError(w, http.StatusForbidden, xystrings.Default.Server.Board.KeymetaOwnerOnly())
		return
	}
	var req sendPassphraseRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.Passphrase == "" {
		httpError(w, http.StatusBadRequest, "empty passphrase")
		return
	}
	var tg sql.NullInt64
	var sentAt sql.NullString
	var name string
	err := s.db.QueryRowContext(r.Context(), `
select u.telegram_user_id, b.passphrase_sent_at, b.name
from boards b join users u on u.id = ? where b.id = ?`, uid, bid).Scan(&tg, &sentAt, &name)
	if handleErr(w, err) {
		return
	}
	if req.UnsentOnly && sentAt.Valid {
		writeJSON(w, sendPassphraseResponse{Kept: true})
		return
	}
	// No bot on this instance, or an account that never linked Telegram: there
	// is nowhere to send to, which is an answer and not an error.
	if s.bot == nil || !tg.Valid {
		writeJSON(w, sendPassphraseResponse{Kept: sentAt.Valid})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), notifySendTimeout)
	defer cancel()
	text := xystrings.Default.Notify.Passphrase.Text(html.EscapeString(name), html.EscapeString(req.Passphrase)) +
		"\n\n" + xystrings.Default.Notify.Passphrase.Keep()
	if _, err := s.bot.Call(ctx, "sendMessage", map[string]any{
		"chat_id": tg.Int64, "text": text, "parse_mode": "HTML",
	}); err != nil {
		// The error is Telegram's description or a transport failure; neither
		// carries the message text, so it is safe to log.
		log.Printf("passphrase to telegram, board %d: %v", bid, err)
		writeJSON(w, sendPassphraseResponse{Kept: sentAt.Valid})
		return
	}
	err = s.withWriteTx(r.Context(), "passphrase-sent", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update boards set passphrase_sent_at = ? where id = ?`, rfc3339(time.Now()), bid)
		return err
	})
	if handleErr(w, err) {
		return
	}
	writeJSON(w, sendPassphraseResponse{Sent: true, Kept: true})
}
