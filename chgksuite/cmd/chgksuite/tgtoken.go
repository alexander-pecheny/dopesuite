package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	chgkstrings "code.pecheny.me/pecheny/dopesuite/chgksuite/i18nstrings"
)

// The bot token lives where chgksuite keeps it, as bot_token at the top of
// ~/.chgksuite/telegram.toml, so the two tools share it. A run without one
// asks for it once and saves it there.

const tokenKey = "bot_token"

// botToken is --token, then $CHGKSUITE_TG_TOKEN, then the saved one, and
// failing those it asks.
func botToken(flag string) (string, error) {
	if t := strings.TrimSpace(flag); t != "" {
		return t, nil
	}
	if t := strings.TrimSpace(os.Getenv("CHGKSUITE_TG_TOKEN")); t != "" {
		return t, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, ".chgksuite", "telegram.toml")
	raw, _ := os.ReadFile(path)
	lines := strings.Split(string(raw), "\n")
	top := len(lines)
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			top = i
			break
		}
		if key, value, ok := strings.Cut(line, "="); ok && strings.TrimSpace(key) == tokenKey {
			if t := unquote(strings.TrimSpace(value)); t != "" {
				return t, nil
			}
		}
	}
	s := chgkstrings.Default
	t, err := prompt(s.Chgkcli.Telegram.TokenPrompt())
	if err != nil || t == "" {
		return "", fmt.Errorf("%s", s.Chgkcli.Telegram.NoToken())
	}
	// A table may follow, so the key goes above the first one.
	lines = append(lines[:top], append([]string{tokenKey + " = " + fmt.Sprintf("%q", t)}, lines[top:]...)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		return "", err
	}
	reportNote("%s", s.Chgkcli.Telegram.TokenSaved(path))
	return t, nil
}
