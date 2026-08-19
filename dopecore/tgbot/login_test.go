package tgbot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	const code = "2TDR7XMG6VEREBQWFUNA"
	cases := []struct {
		name string
		text string
		want Intent
	}{
		{"deep-link start carries the code", "/start " + code, Intent{Kind: IntentRegister, Code: code}},
		{"deep-link start lowercased code", "/start 2tdr7xmg6verebqwfuna", Intent{Kind: IntentRegister, Code: code}},
		{"group start@botname with code", "/start@xy_pecheny_bot " + code, Intent{Kind: IntentRegister, Code: code}},
		{"bare start points at site", "/start", Intent{Kind: IntentLogin}},
		{"login points at site", "/login", Intent{Kind: IntentLogin}},
		{"unknown command points at site", "/help", Intent{Kind: IntentLogin}},
		{"pasted code registers", code, Intent{Kind: IntentRegister, Code: code}},
		{"empty ignored", "   ", Intent{Kind: IntentIgnore}},
		{"chatter is help, not a server call", "привет, это xy?", Intent{Kind: IntentHelp}},
		{"start with junk is help", "/start hi!", Intent{Kind: IntentHelp}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.text); got != tc.want {
				t.Fatalf("Classify(%q) = %+v, want %+v", tc.text, got, tc.want)
			}
		})
	}
}

// The handler's three replies: the server's text for a code, the app's help
// for chatter (no round trip), the app's down text when the server errs.
func TestLoginHandlerReplies(t *testing.T) {
	var calls, sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			_ = r.ParseForm()
			sent = append(sent, r.Form.Get("text"))
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		calls = append(calls, r.URL.Path)
		if r.Header.Get("X-Bot-Secret") != "s3" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path == "/api/telegram/register" && body["code"] != "ABCD2345" {
			http.Error(w, "wrong code", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "ok:" + r.URL.Path})
	}))
	defer srv.Close()

	c := New(Config{Token: "t", APIBase: srv.URL})
	h := LoginHandler(NewBridge(srv.URL, "s3", srv.Client()), Texts{Help: "help", Down: "down"})
	msg := func(text string) Update {
		return Update{Message: &Message{Text: text, Chat: &Chat{ID: 7}, From: &User{ID: 42, Username: "u"}}}
	}
	h(context.Background(), c, msg("ABCD2345"))
	h(context.Background(), c, msg("/login"))
	h(context.Background(), c, msg("what?"))
	h(context.Background(), c, msg(""))
	bad := LoginHandler(NewBridge(srv.URL, "wrong", srv.Client()), Texts{Help: "help", Down: "down"})
	bad(context.Background(), c, msg("ABCD2345"))

	want := []string{"ok:/api/telegram/register", "ok:/api/telegram/login", "help", "down"}
	if len(sent) != len(want) {
		t.Fatalf("sent %v, want %v", sent, want)
	}
	for i := range want {
		if sent[i] != want[i] {
			t.Errorf("reply %d = %q, want %q", i, sent[i], want[i])
		}
	}
	if len(calls) != 3 {
		t.Errorf("server calls = %v: chatter and empty text must not round-trip", calls)
	}
}
