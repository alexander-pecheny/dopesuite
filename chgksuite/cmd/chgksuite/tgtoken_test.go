package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBotTokenIsAskedForOnceAndSaved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CHGKSUITE_TG_TOKEN", "")
	path := filepath.Join(home, ".chgksuite", "telegram.toml")
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, []byte("[bot_tokens]\nother = \"x\"\n"), 0o600)

	read, write, _ := os.Pipe()
	write.WriteString("123:abc\n")
	write.Close()
	stdin := os.Stdin
	os.Stdin = read
	defer func() { os.Stdin = stdin }()

	if got, err := botToken(""); err != nil || got != "123:abc" {
		t.Fatalf("asked: %q, %v", got, err)
	}
	saved, _ := os.ReadFile(path)
	if want := "bot_token = \"123:abc\"\n[bot_tokens]\nother = \"x\"\n"; string(saved) != want {
		t.Fatalf("saved %q, want %q", saved, want)
	}
	// Nothing is left on stdin, so a second ask would fail.
	if got, err := botToken(""); err != nil || got != "123:abc" {
		t.Fatalf("saved: %q, %v", got, err)
	}
	if got, _ := botToken("flag"); got != "flag" {
		t.Fatalf("--token: %q", got)
	}
}
