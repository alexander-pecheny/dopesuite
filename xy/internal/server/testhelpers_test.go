package server

import (
	"context"
	"database/sql"
	"encoding/base64"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func enc(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
func dec(s string) string { b, _ := base64.StdEncoding.DecodeString(s); return string(b) }
func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// simulateBotRegister mimics the telegram bridge consuming a register code.
func (s *server) simulateBotRegister(ctx context.Context, code string, tgUserID int64, tgUsername string) error {
	now := time.Now()
	return s.withWriteTx(ctx, "test-bot-register", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
update telegram_login_codes
set telegram_user_id = ?, telegram_username = ?, consumed_at = ?
where code = ? and kind = 'register' and consumed_at is null and expires_at > ?`,
			tgUserID, tgUsername, rfc3339(now), code, rfc3339(now))
		return err
	})
}

// registerUser provisions a fresh logged-in client via an invite + simulated bot.
func registerUser(t *testing.T, srv *server, ts *httptest.Server, tgUserID int64, name string) *apiClient {
	t.Helper()
	ctx := context.Background()
	invite, err := srv.mintInvite(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c := &apiClient{t: t, base: ts.URL}
	resp := c.do("POST", "/api/auth/register/start", map[string]string{"invite_code": invite})
	mustStatus(t, resp, 200)
	var rs registerStartResponse
	c.decode(resp, &rs)
	if err := srv.simulateBotRegister(ctx, rs.Code, tgUserID, name); err != nil {
		t.Fatal(err)
	}
	resp = c.do("GET", "/api/auth/register/status?code="+rs.Code, nil)
	mustStatus(t, resp, 200)
	return c
}
