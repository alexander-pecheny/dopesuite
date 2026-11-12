package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	chgkstrings "code.pecheny.me/pecheny/dopesuite/chgksuite/i18nstrings"
)

// The bot tokens live where chgksuite keeps them, in ~/.chgksuite/telegram.toml,
// so the two tools share them: bot_token at the top for the default account,
// and a [bot_tokens] table for the others --tgaccount names. A run without one
// asks for it once and saves it there.

const (
	defaultAccount = "my_account"
	tokenKey       = "bot_token"
	tokensTable    = "[bot_tokens]"
)

// botToken is --token, then $CHGKSUITE_TG_TOKEN, then the account's saved one,
// and failing those it asks.
func botToken(flag, account string) (string, error) {
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
	var lines []string
	if text := strings.TrimRight(string(raw), "\n"); text != "" {
		lines = strings.Split(text, "\n")
	}
	table, key := "", tokenKey
	if account != defaultAccount {
		table, key = tokensTable, account
	}
	// at is where a new key goes: the end of its table, before the next one.
	in, at := table == "", -1
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			if in {
				at = i
			}
			in = line == table
			continue
		}
		if k, v, ok := strings.Cut(line, "="); in && ok && unquote(strings.TrimSpace(k)) == key {
			if t := unquote(strings.TrimSpace(v)); t != "" {
				return t, nil
			}
		}
	}
	if at < 0 && in {
		at = len(lines)
	}
	s := chgkstrings.Default
	t, err := prompt(s.Chgkcli.Telegram.TokenPrompt())
	if err != nil || t == "" {
		return "", fmt.Errorf("%s", s.Chgkcli.Telegram.NoToken())
	}
	entry := fmt.Sprintf("%s = %q", tomlKey(key), t)
	if at < 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, table)
		at = len(lines)
	}
	for at > 0 && strings.TrimSpace(lines[at-1]) == "" {
		at--
	}
	lines = append(lines[:at], append([]string{entry}, lines[at:]...)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return "", err
	}
	reportNote("%s", s.Chgkcli.Telegram.TokenSaved(path))
	return t, nil
}

// tomlKey quotes an account name that a bare TOML key cannot spell.
func tomlKey(k string) string {
	for _, r := range k {
		if !(r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return fmt.Sprintf("%q", k)
		}
	}
	return k
}
