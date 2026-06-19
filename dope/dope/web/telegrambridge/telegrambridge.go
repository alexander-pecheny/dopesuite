package telegrambridge

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"dope/dope/platform/session"
	"dope/dope/platform/util"
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
	TelegramBridgeRegisterURL = "https://dope.pecheny.me/register"

	TelegramBridgeGenericError    = "Произошла ошибка. Попробуй еще раз через минуту."
	TelegramBridgeRegisterSuccess = "Готово! Вернись на сайт — там уже видна твоя регистрация."
	TelegramBridgeLoginNeedInvite = "Сначала зарегистрируйся по инвайту: " + TelegramBridgeRegisterURL
	TelegramBridgeLoginExhausted  = "Не получилось выдать код, попробуй еще раз."
	TelegramBridgeLoginCodeMsg    = "Твой код для входа:\n<code>%s</code>\nВведи его на странице входа после логина в течение минуты."

	TelegramBridgeCodeMissing  = "Такого кода нет. Проверь, что скопировал его без пробелов и не дольше минуты прошло."
	TelegramBridgeCodeConsumed = "Этот код уже использован. Запроси новый на сайте."
	TelegramBridgeCodeWrong    = "Этот код не для регистрации. Открой " + TelegramBridgeRegisterURL + " и начни заново."
	TelegramBridgeCodeExpired  = "Срок действия кода истек. Запроси новый на " + TelegramBridgeRegisterURL + "."
)

type telegramRegisterRequest struct {
	Code             string `json:"code"`
	TelegramUserID   int64  `json:"telegram_user_id"`
	TelegramUsername string `json:"telegram_username"`
}

type telegramLoginRequest struct {
	TelegramUserID   int64  `json:"telegram_user_id"`
	TelegramUsername string `json:"telegram_username"`
}

type TelegramBridgeResponse struct {
	Message string `json:"message"`
}

// authorizeBot gates the bot bridge with the shared secret. When DOPE_BOT_SECRET
// is unset the bridge is disabled outright (503) so the code-issuing endpoints
// are never open to unauthenticated callers.
func (s *Server) authorizeBot(w http.ResponseWriter, r *http.Request) bool {
	if s.h.BotSecret() == "" {
		http.Error(w, "telegram bridge disabled", http.StatusServiceUnavailable)
		return false
	}
	got := r.Header.Get("X-Bot-Secret")
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.h.BotSecret())) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
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
	var req telegramRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	msg := s.TelegramConsumeRegister(r.Context(), strings.ToUpper(strings.TrimSpace(req.Code)), req.TelegramUserID, req.TelegramUsername)
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
	var req telegramLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	msg := s.TelegramIssueLogin(r.Context(), req.TelegramUserID, req.TelegramUsername)
	s.h.WriteJSONValue(w, TelegramBridgeResponse{Message: msg})
}

// TelegramConsumeRegister marks a pending 'register' code as consumed by the
// telegram account that sent it. Returns the user-facing reply text.
func (s *Server) TelegramConsumeRegister(ctx context.Context, code string, tgUserID int64, tgUsername string) string {
	if !looksLikeTelegramRegisterCode(code) {
		return TelegramBridgeCodeMissing
	}
	// Serialize through the global write mutex like the game-state path, so a
	// rare bot write never contends with rapid edits at the SQLite level (only
	// one writer; without this, both would race busy_timeout and could fail).
	s.h.Lock()
	defer s.h.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.h.DB().ExecContext(ctx, `
update telegram_login_codes
set telegram_user_id = ?, telegram_username = ?, consumed_at = ?
where code = ?
  and kind = 'register'
  and consumed_at is null
  and expires_at > ?`, tgUserID, tgUsername, now, code, now)
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

// TelegramIssueLogin issues a fresh one-time login code for a registered
// telegram account. Returns the user-facing reply text.
func (s *Server) TelegramIssueLogin(ctx context.Context, tgUserID int64, tgUsername string) string {
	// Hold the global write mutex across the read-modify-write (lookup user,
	// then insert the code), matching the game-state path's serialization.
	s.h.Lock()
	defer s.h.Unlock()
	var userID int64
	err := s.h.DB().QueryRowContext(ctx, `select id from users where telegram_user_id = ? and is_system = 0`, tgUserID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return TelegramBridgeLoginNeedInvite
	}
	if err != nil {
		log.Printf("telegram login lookup user %d: %v", tgUserID, err)
		return TelegramBridgeGenericError
	}

	now := time.Now().UTC()
	createdAt := now.Format(time.RFC3339)
	expires := now.Add(session.TelegramAuthLifetime).Format(time.RFC3339)

	for attempt := 0; attempt < 3; attempt++ {
		code, err := s.h.NewTelegramLoginCode()
		if err != nil {
			log.Printf("telegram login gen code: %v", err)
			return TelegramBridgeGenericError
		}
		_, err = s.h.DB().ExecContext(ctx, `
insert into telegram_login_codes(code, kind, user_id, telegram_user_id, telegram_username, created_at, expires_at)
values(?, 'login', ?, ?, ?, ?, ?)`, code, userID, tgUserID, tgUsername, createdAt, expires)
		if err == nil {
			return fmt.Sprintf(TelegramBridgeLoginCodeMsg, code)
		}
		if !util.IsUniqueViolation(err) {
			log.Printf("telegram login issue: %v", err)
			return TelegramBridgeGenericError
		}
	}
	return TelegramBridgeLoginExhausted
}

// looksLikeTelegramRegisterCode is a cheap shape check (base32 alphabet, sane
// length) mirroring the bot's old local triage, so an obviously-bogus message
// never hits the database.
func looksLikeTelegramRegisterCode(s string) bool {
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
