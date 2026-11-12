package main

import (
	"os"
	"path/filepath"
	"testing"
)

// answer puts text on stdin for the prompt to read.
func answer(t *testing.T, text string) {
	read, write, _ := os.Pipe()
	write.WriteString(text)
	write.Close()
	stdin := os.Stdin
	os.Stdin = read
	t.Cleanup(func() { os.Stdin = stdin })
}

// The file is chgksuite's own telegram.toml, so what is saved has to read back
// in Python's layout: bot_token at the top, the other accounts in [bot_tokens].
func TestBotTokenIsAskedForOnceAndSaved(t *testing.T) {
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
			answer(t, "1:a\n")
			if got, err := botToken("", c.account); err != nil || got != "1:a" {
				t.Fatalf("asked: %q, %v", got, err)
			}
			if saved, _ := os.ReadFile(path); string(saved) != c.after {
				t.Fatalf("saved %q, want %q", saved, c.after)
			}
			// stdin is spent, so this one must come from the file.
			if got, err := botToken("", c.account); err != nil || got != "1:a" {
				t.Fatalf("read back: %q, %v", got, err)
			}
		})
	}
}
