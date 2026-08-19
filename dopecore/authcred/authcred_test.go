package authcred

import (
	"strings"
	"testing"
	"time"
)

func TestVerifyPasswordUpgrading(t *testing.T) {
	bc, err := HashPassword("hunter22")
	if err != nil {
		t.Fatal(err)
	}
	legacy := LegacySHA256Password("hunter22", "salty")
	cases := []struct {
		name, hash, salt, pw string
		ok, upgrades, fails  bool
	}{
		{"empty hash", "", "", "hunter22", false, false, false},
		{"bcrypt match", bc, "", "hunter22", true, false, false},
		{"bcrypt mismatch", bc, "", "hunter23", false, false, false},
		{"bcrypt garbage", "$2a$garbage", "", "hunter22", false, false, true},
		{"legacy match upgrades", legacy, "salty", "hunter22", true, true, false},
		{"legacy wrong salt", legacy, "pepper", "hunter22", false, false, false},
		{"legacy mismatch", legacy, "salty", "hunter23", false, false, false},
	}
	for _, c := range cases {
		ok, up, err := VerifyPasswordUpgrading(c.hash, c.salt, c.pw)
		if (err != nil) != c.fails || ok != c.ok || (up != "") != c.upgrades {
			t.Errorf("%s: ok=%v upgraded=%q err=%v", c.name, ok, up, err)
		}
		if up != "" && !VerifyPassword(up, c.pw) {
			t.Errorf("%s: the upgraded hash does not verify", c.name)
		}
	}
}

func TestVerifyPasswordRejectsEmptyHash(t *testing.T) {
	if VerifyPassword("", "") {
		t.Fatal("empty hash verified")
	}
}

func TestNewTelegramLoginCodeShape(t *testing.T) {
	code, err := NewTelegramLoginCode()
	if err != nil || len(code) != TelegramLoginCodeLen || strings.Trim(code, TelegramLoginCodeAlphabet) != "" {
		t.Fatalf("code %q err %v", code, err)
	}
}

func TestNeedsRefresh(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	full := now.Add(30 * 24 * time.Hour)
	cases := []struct {
		name     string
		lastSeen time.Time
		expiry   time.Time
		want     bool
	}{
		{"never seen", time.Time{}, full, true},
		{"seen just now", now.Add(-10 * time.Second), full, false},
		{"seen a minute ago", now.Add(-time.Minute), full, true},
		{"expiry shortened", now.Add(-10 * time.Second), full.Add(-2 * time.Minute), true},
		{"no expiry known", now.Add(-10 * time.Second), time.Time{}, false},
	}
	for _, c := range cases {
		if got := NeedsRefresh(c.lastSeen, c.expiry, now); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
