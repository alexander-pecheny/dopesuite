package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"code.pecheny.me/pecheny/dopesuite/dopecore/tgbot"
)

type sentMessage struct {
	ChatID    int64  `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

// recordingBot stands in for Telegram and keeps every sendMessage. While
// failing is set, it answers the way Telegram answers a chat the bot cannot
// write to.
type recordingBot struct {
	mu      sync.Mutex
	sent    []sentMessage
	failing bool
}

func (b *recordingBot) messages() []sentMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]sentMessage(nil), b.sent...)
}

func installRecordingBot(t *testing.T, srv *server) *recordingBot {
	t.Helper()
	rec := &recordingBot{}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
			return
		}
		rec.mu.Lock()
		defer rec.mu.Unlock()
		if rec.failing {
			_, _ = w.Write([]byte(`{"ok":false,"description":"Forbidden: bot was blocked by the user"}`))
			return
		}
		var m sentMessage
		_ = json.NewDecoder(r.Body).Decode(&m)
		rec.sent = append(rec.sent, m)
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	t.Cleanup(api.Close)
	srv.bot = tgbot.New(tgbot.Config{Token: "stub", APIBase: api.URL, PollTimeout: time.Minute})
	return rec
}

func sendPassphrase(t *testing.T, c *apiClient, board, pass string, unsentOnly bool, wantStatus int) sendPassphraseResponse {
	t.Helper()
	resp := c.do("POST", "/api/boards/"+board+"/passphrase/telegram", map[string]any{"passphrase": pass, "unsent_only": unsentOnly})
	mustStatus(t, resp, wantStatus)
	var out sendPassphraseResponse
	if wantStatus == http.StatusOK {
		c.decode(resp, &out)
	}
	return out
}

func TestPassphraseGoesToTheOwnersTelegram(t *testing.T) {
	ts, srv := newTestServer(t)
	rec := installRecordingBot(t, srv)
	owner := registerUser(t, srv, ts, 881001, "owner")
	board := newBoard(t, owner, "Тур <1> & co")
	const pass = "отвёртка-глобус-<b>-лампа"

	got := sendPassphrase(t, owner, board, pass, false, http.StatusOK)
	if !got.Sent || !got.Kept {
		t.Fatalf("first send: %+v", got)
	}
	msgs := rec.messages()
	if len(msgs) != 1 || msgs[0].ChatID != 881001 || msgs[0].ParseMode != "HTML" {
		t.Fatalf("messages: %+v", msgs)
	}
	// Both values are escaped: neither the board name nor the passphrase may be
	// read as markup.
	for _, want := range []string{"«Тур &lt;1&gt; &amp; co»", "<code>отвёртка-глобус-&lt;b&gt;-лампа</code>"} {
		if !strings.Contains(msgs[0].Text, want) {
			t.Errorf("message lacks %q:\n%s", want, msgs[0].Text)
		}
	}
	var stored string
	if err := srv.db.QueryRow(`select coalesce(passphrase_sent_at, '') from boards where id = ?`, board).Scan(&stored); err != nil || stored == "" {
		t.Fatalf("passphrase_sent_at not stamped (%v)", err)
	}

	// The monthly check's typed path does not send a copy already sent.
	got = sendPassphrase(t, owner, board, pass, true, http.StatusOK)
	if got.Sent || !got.Kept || len(rec.messages()) != 1 {
		t.Fatalf("unsent_only after a send: %+v, %d messages", got, len(rec.messages()))
	}

	// A passphrase change makes the copy stale, so the typed path sends again.
	resp := owner.do("PUT", "/api/boards/"+board+"/keymeta", map[string]string{
		"kdf_salt": enc("s2"), "kdf_params": `{"kdf":"scrypt","N":65536,"r":8,"p":1}`, "wrapped_key": enc("w2"),
	})
	mustStatus(t, resp, http.StatusNoContent)
	got = sendPassphrase(t, owner, board, "новый-пароль-для-доски", true, http.StatusOK)
	if !got.Sent || len(rec.messages()) != 2 {
		t.Fatalf("unsent_only after a change: %+v, %d messages", got, len(rec.messages()))
	}
}

func TestPassphraseSendIsTheOwnersOnly(t *testing.T) {
	ts, srv := newTestServer(t)
	rec := installRecordingBot(t, srv)
	owner := registerUser(t, srv, ts, 881011, "owner")
	editor := registerUser(t, srv, ts, 881012, "editor")
	board := newBoard(t, owner, "b")
	var bid int64
	if err := srv.db.QueryRow(`select id from boards where name = 'b'`).Scan(&bid); err != nil {
		t.Fatal(err)
	}
	addBoardMember(t, srv, bid, meUserID(t, editor))

	sendPassphrase(t, editor, board, "кто-то-чужой-пароль", false, http.StatusForbidden)
	if n := len(rec.messages()); n != 0 {
		t.Fatalf("an editor's request sent %d messages", n)
	}
}

func TestPassphraseWithNowhereToSend(t *testing.T) {
	ts, srv := newTestServer(t)
	rec := installRecordingBot(t, srv)
	owner := registerUser(t, srv, ts, 881021, "owner")
	board := newBoard(t, owner, "b")

	// Telegram refuses (the owner blocked the bot): nothing is stamped.
	rec.mu.Lock()
	rec.failing = true
	rec.mu.Unlock()
	if got := sendPassphrase(t, owner, board, "раз-два-три-четыре", false, http.StatusOK); got.Sent || got.Kept {
		t.Fatalf("refused by telegram: %+v", got)
	}

	// An account with no Telegram at all.
	if _, err := srv.db.Exec(`update users set telegram_user_id = null where telegram_user_id = 881021`); err != nil {
		t.Fatal(err)
	}
	rec.mu.Lock()
	rec.failing = false
	rec.mu.Unlock()
	if got := sendPassphrase(t, owner, board, "раз-два-три-четыре", false, http.StatusOK); got.Sent || got.Kept {
		t.Fatalf("no telegram: %+v", got)
	}
	if n := len(rec.messages()); n != 0 {
		t.Fatalf("%d messages with nowhere to send", n)
	}
}
