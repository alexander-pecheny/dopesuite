package tests

import (
	"context"
	dopeserver "dope/dope/server"
	"testing"
	"time"

	"pecheny.me/dopecore/tgbot"

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

// The bot now holds this conversation in the server process: a pasted code goes
// straight to the registrar, with no secret and no hop in between.
func TestTelegramBotRegistersThroughTheServer(t *testing.T) {
	s := newAuthTestServer(t)
	seedRegisterCode(t, s, "ABCD2345", time.Now().Add(time.Minute))

	reg := s.BotRegistrar()
	msg, err := reg.Register(context.Background(), "abcd2345", tgbot.From{UserID: 777, Username: "alice"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if msg != telegrambridge.TelegramBridgeRegisterSuccess {
		t.Fatalf("message = %q, want success", msg)
	}

	// The row must now be consumed by the telegram account (the lowercase code
	// the user pasted was upper-cased on the way in).
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
	if got := s.TgBridge().TelegramConsumeRegister(ctx, "ZZZZ2345", 1, "x", ""); got != telegrambridge.TelegramBridgeCodeMissing {
		t.Fatalf("unknown = %q, want missing", got)
	}
	// Non-code shape -> missing (never hits the DB).
	if got := s.TgBridge().TelegramConsumeRegister(ctx, "!!", 1, "x", ""); got != telegrambridge.TelegramBridgeCodeMissing {
		t.Fatalf("bogus = %q, want missing", got)
	}
	// Expired.
	seedRegisterCode(t, s, "EXPIRED2", time.Now().Add(-time.Minute))
	if got := s.TgBridge().TelegramConsumeRegister(ctx, "EXPIRED2", 1, "x", ""); got != telegrambridge.TelegramBridgeCodeExpired {
		t.Fatalf("expired = %q, want expired", got)
	}
	// Success then already-consumed.
	seedRegisterCode(t, s, "FRESH234", time.Now().Add(time.Minute))
	if got := s.TgBridge().TelegramConsumeRegister(ctx, "FRESH234", 5, "y", ""); got != telegrambridge.TelegramBridgeRegisterSuccess {
		t.Fatalf("first consume = %q, want success", got)
	}
	if got := s.TgBridge().TelegramConsumeRegister(ctx, "FRESH234", 5, "y", ""); got != telegrambridge.TelegramBridgeCodeConsumed {
		t.Fatalf("second consume = %q, want consumed", got)
	}
}

func TestTelegramBridgeIssueLogin(t *testing.T) {
	s := newAuthTestServer(t)
	ctx := context.Background()

	// A bare /start (no code) points the user at the site code, whether or not
	// the telegram account exists — login and registration share that flow.
	if got := s.TgBridge().TelegramIssueLogin(ctx, 9999, "ghost"); got != telegrambridge.TelegramBridgeLoginOnSite {
		t.Fatalf("unknown user = %q, want on-site pointer", got)
	}
	seedTelegramUser(t, s, 4242, "bob")
	if got := s.TgBridge().TelegramIssueLogin(ctx, 4242, "bob"); got != telegrambridge.TelegramBridgeLoginOnSite {
		t.Fatalf("issue login = %q, want on-site pointer", got)
	}
	// No login code is issued anymore.
	var n int
	if err := s.Eng().DB.QueryRow(`select count(*) from telegram_login_codes where kind='login' and telegram_user_id=4242`).Scan(&n); err != nil {
		t.Fatalf("count login codes: %v", err)
	}
	if n != 0 {
		t.Fatalf("login codes = %d, want 0", n)
	}
}
