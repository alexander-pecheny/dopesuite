package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The file is chgksuite's own telegram.toml, so what is saved has to read back
// in Python's layout: bot_token at the top, the other accounts in [bot_tokens].
func TestBotTokenIsSavedOnceGiven(t *testing.T) {
	for _, c := range []struct{ name, before, account, after string }{
		{"no file", "", defaultAccount, "bot_token = \"1:a\"\n"},
		{"above a table", "[bot_tokens]\nother = \"x\"\n", defaultAccount, "bot_token = \"1:a\"\n[bot_tokens]\nother = \"x\"\n"},
		{"into the table", "bot_token = \"0:z\"\n\n[bot_tokens]\nother = \"x\"\n", "test", "bot_token = \"0:z\"\n\n[bot_tokens]\nother = \"x\"\ntest = \"1:a\"\n"},
		{"a new table", "bot_token = \"0:z\"\n", "test", "bot_token = \"0:z\"\n\n[bot_tokens]\ntest = \"1:a\"\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CHGKSUITE_TG_TOKEN", "")
			path := filepath.Join(home, ".chgksuite", "telegram.toml")
			if c.before != "" {
				os.MkdirAll(filepath.Dir(path), 0o700)
				os.WriteFile(path, []byte(c.before), 0o600)
			}
			if _, err := botToken("", c.account); !errors.Is(err, errNoToken) {
				t.Fatalf("nothing saved: %v, want errNoToken", err)
			}
			if got, err := botToken("1:a", c.account); err != nil || got != "1:a" {
				t.Fatalf("given: %q, %v", got, err)
			}
			if saved, _ := os.ReadFile(path); string(saved) != c.after {
				t.Fatalf("saved %q, want %q", saved, c.after)
			}
			if got, err := botToken("", c.account); err != nil || got != "1:a" {
				t.Fatalf("read back: %q, %v", got, err)
			}
		})
	}
}
