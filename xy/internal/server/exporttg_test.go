package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubTelegram is a Bot API that never has anything to poll and answers the two
// questions the access check asks. admin decides whether the bot is one of the
// administrators of the chat it is about to post to.
func stubTelegram(t *testing.T, admin bool) *httptest.Server {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":42}}`))
		case strings.HasSuffix(r.URL.Path, "/getChatAdministrators"):
			if admin {
				_, _ = w.Write([]byte(`{"ok":true,"result":[{"user":{"id":42}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":[{"user":{"id":7}}]}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
		}
	}))
	t.Cleanup(api.Close)
	return api
}

// postTelegram drives the export endpoint and returns the stream's lines.
func postTelegram(t *testing.T, ts *httptest.Server, c *apiClient, token, channel, chat string) (int, []tgLine) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("source", packSource)
	_ = mw.WriteField("filename", "Тур 1")
	_ = mw.WriteField("token", token)
	_ = mw.WriteField("channel", channel)
	_ = mw.WriteField("chat", chat)
	mw.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/export/telegram", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var lines []tgLine
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil // a refusal before the stream is plain text
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var line tgLine
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("stream line %q: %v", sc.Text(), err)
		}
		lines = append(lines, line)
	}
	return resp.StatusCode, lines
}

// TestExportTelegramNeedsATarget: the three fields are the whole request, and a
// missing one is a plain 400 — the stream only takes over once they are there.
func TestExportTelegramNeedsATarget(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 770220, "tgnone")

	status, _ := postTelegram(t, ts, c, "", "-1001", "-1002")
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
}

// TestExportTelegramRefusesANonAdminBot is the check chgksuite makes and the
// commonest thing to have missed: a bot that is not an administrator cannot post,
// and finding that out before the first question is the point.
func TestExportTelegramRefusesANonAdminBot(t *testing.T) {
	ts, srv := newTestServer(t)
	srv.tgAPIBase = stubTelegram(t, false).URL
	c := registerUser(t, srv, ts, 770221, "tgnotadmin")

	status, lines := postTelegram(t, ts, c, "12345:secret", "-1001", "-1002")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the failure belongs in the stream", status)
	}
	last := lines[len(lines)-1]
	if !strings.Contains(last.Error, "администратор") {
		t.Errorf("last line = %+v, want the not-an-administrator error", last)
	}
	if last.Done {
		t.Error("a refused export must not report itself done")
	}
}
