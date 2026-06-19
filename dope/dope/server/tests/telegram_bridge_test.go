package tests

import (
	"context"
	dopeserver "dope/dope/server"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dope/dope/web/telegrambridge"
)

func seedRegisterCode(t *testing.T, s *dopeserver.Server, code string, expires time.Time) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.Eng().DB.Exec(`
insert into telegram_login_codes(code, kind, created_at, expires_at)
values(?, 'register', ?, ?)`, code, now, expires.UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed register code: %v", err)
	}
}

func seedTelegramUser(t *testing.T, s *dopeserver.Server, tgUserID int64, username string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.Eng().DB.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(?, ?, null, 0, ?, ?)`, tgUserID, username, now, now); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func TestTelegramBridgeSecretGate(t *testing.T) {
	s := newAuthTestServer(t)

	// No secret configured -> bridge disabled (503), even with a header.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/login", strings.NewReader(`{"telegram_user_id":1}`))
	req.Header.Set("X-Bot-Secret", "anything")
	s.TgBridge().HandleTelegramLogin(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled bridge = %d, want 503", rec.Code)
	}

	// Secret set but wrong -> 401.
	s.Eng().BotSecret = "s3kret"
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/telegram/login", strings.NewReader(`{"telegram_user_id":1}`))
	req.Header.Set("X-Bot-Secret", "nope")
	s.TgBridge().HandleTelegramLogin(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret = %d, want 401", rec.Code)
	}

	// Missing header -> 401.
	rec = httptest.NewRecorder()
	s.TgBridge().HandleTelegramLogin(rec, httptest.NewRequest(http.MethodPost, "/api/telegram/login", strings.NewReader(`{"telegram_user_id":1}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no header = %d, want 401", rec.Code)
	}
}

func TestTelegramBridgeRegisterHandler(t *testing.T) {
	s := newAuthTestServer(t)
	s.Eng().BotSecret = "s3kret"
	seedRegisterCode(t, s, "ABCD2345", time.Now().Add(time.Minute))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/register",
		strings.NewReader(`{"code":"abcd2345","telegram_user_id":777,"telegram_username":"alice"}`))
	req.Header.Set("X-Bot-Secret", "s3kret")
	s.TgBridge().HandleTelegramRegister(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("register = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var resp telegrambridge.TelegramBridgeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Message != telegrambridge.TelegramBridgeRegisterSuccess {
		t.Fatalf("message = %q, want success", resp.Message)
	}
	// The row must now be consumed by the telegram account (lowercase code was upper-cased).
	var tgID int64
	var consumed string
	if err := s.Eng().DB.QueryRow(`select telegram_user_id, consumed_at from telegram_login_codes where code = 'ABCD2345'`).Scan(&tgID, &consumed); err != nil {
		t.Fatalf("lookup consumed: %v", err)
	}
	if tgID != 777 || consumed == "" {
		t.Fatalf("row not consumed: tgID=%d consumed=%q", tgID, consumed)
	}
}

func TestTelegramBridgeConsumeRegisterReasons(t *testing.T) {
	s := newAuthTestServer(t)
	ctx := context.Background()

	// Unknown code.
	if got := s.TgBridge().TelegramConsumeRegister(ctx, "ZZZZ2345", 1, "x"); got != telegrambridge.TelegramBridgeCodeMissing {
		t.Fatalf("unknown = %q, want missing", got)
	}
	// Non-code shape -> missing (never hits the DB).
	if got := s.TgBridge().TelegramConsumeRegister(ctx, "!!", 1, "x"); got != telegrambridge.TelegramBridgeCodeMissing {
		t.Fatalf("bogus = %q, want missing", got)
	}
	// Expired.
	seedRegisterCode(t, s, "EXPIRED2", time.Now().Add(-time.Minute))
	if got := s.TgBridge().TelegramConsumeRegister(ctx, "EXPIRED2", 1, "x"); got != telegrambridge.TelegramBridgeCodeExpired {
		t.Fatalf("expired = %q, want expired", got)
	}
	// Success then already-consumed.
	seedRegisterCode(t, s, "FRESH234", time.Now().Add(time.Minute))
	if got := s.TgBridge().TelegramConsumeRegister(ctx, "FRESH234", 5, "y"); got != telegrambridge.TelegramBridgeRegisterSuccess {
		t.Fatalf("first consume = %q, want success", got)
	}
	if got := s.TgBridge().TelegramConsumeRegister(ctx, "FRESH234", 5, "y"); got != telegrambridge.TelegramBridgeCodeConsumed {
		t.Fatalf("second consume = %q, want consumed", got)
	}
}

func TestTelegramBridgeIssueLogin(t *testing.T) {
	s := newAuthTestServer(t)
	ctx := context.Background()

	// Unknown telegram account -> told to register.
	if got := s.TgBridge().TelegramIssueLogin(ctx, 9999, "ghost"); got != telegrambridge.TelegramBridgeLoginNeedInvite {
		t.Fatalf("unknown user = %q, want need-invite", got)
	}

	seedTelegramUser(t, s, 4242, "bob")
	msg := s.TgBridge().TelegramIssueLogin(ctx, 4242, "bob")
	if !strings.Contains(msg, "<code>") {
		t.Fatalf("issue login = %q, want a code", msg)
	}
	// A login code row must now exist for the user.
	var n int
	if err := s.Eng().DB.QueryRow(`select count(*) from telegram_login_codes where kind='login' and telegram_user_id=4242 and consumed_at is null`).Scan(&n); err != nil {
		t.Fatalf("count login codes: %v", err)
	}
	if n != 1 {
		t.Fatalf("login codes = %d, want 1", n)
	}
}
