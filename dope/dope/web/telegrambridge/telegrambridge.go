package telegrambridge

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"pecheny.me/dopecore/tgbridge"
)

// The Telegram login/registration handshake, server side. The bot used to open
// fest.db directly, which made it a second long-lived writer on the live
// database — implicated in the WAL trouble behind the data-loss incident. Then
// it called these as loopback HTTP endpoints behind a shared secret. Now it runs
// in the server process (server/bot.go) and calls them as what they always were:
// two methods that write under the server's own mutex.

const (
	TelegramBridgeLoginURL = "https://dope.pecheny.me/login"

	TelegramBridgeGenericError    = "Произошла ошибка. Попробуй еще раз через минуту."
	TelegramBridgeRegisterSuccess = "Готово! Вернись на сайт — там уже видна твоя регистрация."
	TelegramBridgeLoginOnSite     = "Пришлите код со страницы входа. Если его нет — откройте " + TelegramBridgeLoginURL + " и нажмите «Войти через телеграм»."

	TelegramBridgeCodeMissing  = "Такого кода нет. Проверь, что скопировал его без пробелов и не дольше минуты прошло."
	TelegramBridgeCodeConsumed = "Этот код уже использован. Запроси новый на сайте."
	TelegramBridgeCodeWrong    = "Этот код не для входа. Открой " + TelegramBridgeLoginURL + " и начни заново."
	TelegramBridgeCodeExpired  = "Срок действия кода истек. Запроси новый на " + TelegramBridgeLoginURL + "."
)

// The SQL and the code shape are single-sourced in dopecore/tgbridge; the
// answers stay here because they run under dope's write-mutex discipline and
// carry dope's reply text.

// TelegramConsumeRegister marks a pending 'register' code as consumed by the
// telegram account that sent it. Returns the user-facing reply text.
func (s *Server) TelegramConsumeRegister(ctx context.Context, code string, tgUserID int64, tgUsername, tgName string) string {
	if !tgbridge.LooksLikeRegisterCode(code) {
		return TelegramBridgeCodeMissing
	}
	// Serialize through the global write mutex like the game-state path, so a
	// rare bot write never contends with rapid edits at the SQLite level (only
	// one writer; without this, both would race busy_timeout and could fail).
	s.h.Lock()
	defer s.h.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.h.DB().ExecContext(ctx, tgbridge.ConsumeRegisterSQL, tgUserID, tgUsername, tgName, now, code, now)
	if err != nil {
		log.Printf("telegram register consume %s: %v", code, err)
		return TelegramBridgeGenericError
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return TelegramBridgeRegisterSuccess
	}
	return s.telegramRegisterFailureReason(ctx, code)
}

// telegramRegisterFailureReason explains why a consume missed. The caller
// (TelegramConsumeRegister) already holds s.mu, so this must not re-lock it.
func (s *Server) telegramRegisterFailureReason(ctx context.Context, code string) string {
	var kind string
	var consumedAt sql.NullString
	err := s.h.DB().QueryRowContext(ctx, `
select kind, consumed_at from telegram_login_codes where code = ?`, code).Scan(&kind, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return TelegramBridgeCodeMissing
	}
	if err != nil {
		log.Printf("telegram register lookup %s: %v", code, err)
		return TelegramBridgeGenericError
	}
	if consumedAt.Valid {
		return TelegramBridgeCodeConsumed
	}
	if kind != "register" {
		return TelegramBridgeCodeWrong
	}
	return TelegramBridgeCodeExpired
}

// TelegramIssueLogin answers a bare /start or /login (no code) sent to the bot —
// including a deep-link /start whose payload the client dropped (a known Telegram
// behavior for users who already started the bot). Login and registration both
// run through the code the site shows, so there is nothing to hand back: point
// the user at that code, whether or not they already have an account.
func (s *Server) TelegramIssueLogin(ctx context.Context, tgUserID int64, tgUsername string) string {
	return TelegramBridgeLoginOnSite
}
