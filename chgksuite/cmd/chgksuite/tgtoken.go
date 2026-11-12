package main

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The bot tokens live where chgksuite keeps them, in ~/.chgksuite/telegram.toml,
// so the two tools share them: bot_token at the top for the default account,
// and a [bot_tokens] table for the others --tgaccount names. The CLI never asks
// for one, since agents run it unattended: a --token for an account with none
// saved is saved, and a run with no token at all fails with errNoToken, which
// chgksuite-app answers by asking and running again with --token.

var errNoToken = errors.New("no bot token: pass --token, which is saved for next time, or set CHGKSUITE_TG_TOKEN")

const (
	defaultAccount = "my_account"
	tokenKey       = "bot_token"
	tokensTable    = "[bot_tokens]"
)

// botToken is --token, then $CHGKSUITE_TG_TOKEN, then the account's saved one.
func botToken(flag, account string) (string, error) {
	given := strings.TrimSpace(flag)
	if t := strings.TrimSpace(os.Getenv("CHGKSUITE_TG_TOKEN")); given == "" && t != "" {
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
				return cmp.Or(given, t), nil
			}
		}
	}
	if at < 0 && in {
		at = len(lines)
	}
	if given == "" {
		return "", errNoToken
	}
	entry := fmt.Sprintf("%s = %q", tomlKey(key), given)
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
	reportNote("%s", fmt.Sprintf("token saved to %s", path))
	return given, nil
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
