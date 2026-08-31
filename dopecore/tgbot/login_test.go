package tgbot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

// fakeRegistrar records what the conversation asked of the server.
type fakeRegistrar struct {
	calls []string
	err   error
}

func (f *fakeRegistrar) Register(_ context.Context, code string, from From) (string, error) {
	f.calls = append(f.calls, "register:"+code)
	return "ok:register", f.err
}

func (f *fakeRegistrar) Login(_ context.Context, from From) (string, error) {
	f.calls = append(f.calls, "login")
	return "ok:login", f.err
}

// The handler's three replies: the server's text for a code, the app's help
// for chatter (no round trip), the app's down text when the server errs.
func TestLoginHandlerReplies(t *testing.T) {
	var sent []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		sent = append(sent, r.Form.Get("text"))
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer api.Close()

	c := New(Config{Token: "t", APIBase: api.URL})
	reg := &fakeRegistrar{}
	h := LoginHandler(reg, Texts{Help: "help", Down: "down"})
	msg := func(text string) Update {
		return Update{Message: &Message{Text: text, Chat: &Chat{ID: 7}, From: &User{ID: 42, Username: "u"}}}
	}
	h(context.Background(), c, msg("ABCD2345"))
	h(context.Background(), c, msg("/login"))
	h(context.Background(), c, msg("what?"))
	h(context.Background(), c, msg(""))
	LoginHandler(&fakeRegistrar{err: errors.New("down")}, Texts{Help: "help", Down: "down"})(
		context.Background(), c, msg("ABCD2345"))

	want := []string{"ok:register", "ok:login", "help", "down"}
	if len(sent) != len(want) {
		t.Fatalf("sent %v, want %v", sent, want)
	}
	for i := range want {
		if sent[i] != want[i] {
			t.Errorf("reply %d = %q, want %q", i, sent[i], want[i])
		}
	}
	if len(reg.calls) != 2 {
		t.Errorf("registrar calls = %v: chatter and empty text must not round-trip", reg.calls)
	}
}
