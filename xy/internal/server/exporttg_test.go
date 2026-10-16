package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// stubTelegram is a Bot API that never has anything to poll. getChat reports
// linked as the channel's discussion group (0 is none), and the bot is an
// administrator of exactly the chats in admins.
func stubTelegram(t *testing.T, linked int64, admins ...string) *httptest.Server {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChatID string `json:"chat_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":42}}`))
		case strings.HasSuffix(r.URL.Path, "/getChat"):
			_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"id":%s,"linked_chat_id":%d}}`, body.ChatID, linked)
		case strings.HasSuffix(r.URL.Path, "/getChatAdministrators"):
			if slices.Contains(admins, body.ChatID) {
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
func postTelegram(t *testing.T, ts *httptest.Server, c *apiClient, token, channel string) (int, []tgLine) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("source", packSource)
	_ = mw.WriteField("filename", "Тур 1")
	_ = mw.WriteField("token", token)
	_ = mw.WriteField("channel", channel)
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

// TestExportTelegramNeedsATarget: the token and the channel are the whole
// request, and a missing one is a plain 400 — the stream only takes over once
// they are there.
func TestExportTelegramNeedsATarget(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 770220, "tgnone")

	status, _ := postTelegram(t, ts, c, "", "-1001")
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
}

// TestExportTelegramRefuses: the group is the one the channel names, and the
// bot must administer both before the first question goes out. Each refusal
// arrives in the stream, since the header is already sent.
func TestExportTelegramRefuses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		linked int64
		admins []string
		want   string
	}{
		{"not an admin of the channel", 1002, nil, "не администратор канала"},
		{"no discussion group", 0, []string{"-1001"}, "нет группы обсуждения"},
		{"not an admin of the group", 1002, []string{"-1001"}, "не администратор группы"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts, srv := newTestServer(t)
			srv.tgAPIBase = stubTelegram(t, tc.linked, tc.admins...).URL
			c := registerUser(t, srv, ts, 770221, "tgrefused")

			status, lines := postTelegram(t, ts, c, "12345:secret", "-1001")
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200 — the failure belongs in the stream", status)
			}
			last := lines[len(lines)-1]
			if !strings.Contains(last.Error, tc.want) {
				t.Errorf("last line = %+v, want an error with %q", last, tc.want)
			}
			if last.Done {
				t.Error("a refused export must not report itself done")
			}
		})
	}
}
