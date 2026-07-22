package telegrambridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"pecheny.me/dopecore/tgbridge"
)

// telegram_bridge.go is the server side of the Telegram login/registration
// handshake. The bot used to open fest.db directly (read+write) to consume
// register codes and issue login codes, which made it a second long-lived
// writer/connection on the live database — implicated in the WAL checkpoint/
// recovery trouble behind the data-loss incident. Instead the bot now holds NO
// database handle and calls these endpoints; the server stays the sole owner of
// fest.db. The endpoints are gated by a shared secret (DOPE_BOT_SECRET) so only
// the co-located bot can drive them. Behavior mirrors the bot's old SQL exactly.

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

// The wire protocol — request/response shapes, the shared-secret gate, the SQL —
// is single-sourced in dopecore/tgbridge. The handlers stay here because they run
// under dope's own write-mutex discipline and carry dope's reply text.
type TelegramBridgeResponse = tgbridge.Response

// authorizeBot gates the bot bridge with the shared secret. When DOPE_BOT_SECRET
// is unset the bridge is disabled outright (503) so the code-issuing endpoints
// are never open to unauthenticated callers.
func (s *Server) authorizeBot(w http.ResponseWriter, r *http.Request) bool {
	ok, configured := tgbridge.SecretOK(r, s.h.BotSecret())
	switch {
	case !configured:
		http.Error(w, "telegram bridge disabled", http.StatusServiceUnavailable)
	case !ok:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
	return ok && configured
}

func (s *Server) HandleTelegramRegister(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeBot(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	var req tgbridge.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	msg := s.TelegramConsumeRegister(r.Context(), strings.ToUpper(strings.TrimSpace(req.Code)), req.TelegramUserID, req.TelegramUsername, req.TelegramName)
	s.h.WriteJSONValue(w, TelegramBridgeResponse{Message: msg})
}

func (s *Server) HandleTelegramLogin(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeBot(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	var req tgbridge.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	msg := s.TelegramIssueLogin(r.Context(), req.TelegramUserID, req.TelegramUsername)
	s.h.WriteJSONValue(w, TelegramBridgeResponse{Message: msg})
}

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
