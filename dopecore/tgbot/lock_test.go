package tgbot

import (
	"strings"
	"testing"
)

func TestTokenHashNamesTheTokenNotTheSecret(t *testing.T) {
	const token = "123456:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"
	h := TokenHash(token)
	if len(h) != 12 {
		t.Fatalf("hash must be 12 hex digits, got %q", h)
	}
	if strings.Contains(token, h) {
		t.Error("the hash must not be a slice of the token")
	}
	if TokenHash(" "+token+"\n") != h {
		t.Error("surrounding whitespace in an env var must not make a second identity")
	}
	if TokenHash(token+"x") == h {
		t.Error("two tokens must not share an identity")
	}
}

// The whole point: the second claim on a token loses, and is told who won.
func TestAcquirePollLockIsExclusive(t *testing.T) {
	lockDir = t.TempDir()
	const token = "one-bot"

	release, err := AcquirePollLock(token)
	if err != nil {
		t.Fatalf("first claim must win: %v", err)
	}

	if _, err := AcquirePollLock(token); err == nil {
		t.Fatal("a second poller on the same token must be refused")
	} else if !strings.Contains(err.Error(), "held by") {
		t.Errorf("the refusal must name the holder, got %v", err)
	}

	if _, err := AcquirePollLock("another-bot"); err != nil {
		t.Errorf("a different token is a different claim: %v", err)
	}

	release()
	release2, err := AcquirePollLock(token)
	if err != nil {
		t.Fatalf("a released claim must be takeable: %v", err)
	}
	release2()
}
