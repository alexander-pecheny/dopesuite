// Package board is chgksuite's `board` (formerly `trello`) command: a folder of
// .4s files kept in step with a Trello-style board. Two services speak the same
// API — Trello itself, and xy, which is end-to-end encrypted, so its fields
// arrive as ciphertext and are opened here with the board's passphrase.
package board

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Service is which of the two a URL names.
type Service string

const (
	Trello Service = "trello"
	XY     Service = "xy"
)

const trelloHost = "trello.com"

// Board is a board to talk to.
type Board struct {
	Service    Service
	Host       string
	ID         string
	BaseURL    string
	Token      string
	Key        string // Trello's app key
	Passphrase string // xy only
}

var reTrelloBoard = regexp.MustCompile(`/b/([^/]+)`)
var reXYBoard = regexp.MustCompile(`/board/([^/?#]+)`)

// ParseURL is board_config.parse_board_url. It takes a Trello board link, an xy
// board link, or a bare Trello id (which is what the legacy .board_id held).
func ParseURL(raw string) (Board, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Board{}, fmt.Errorf("пустая ссылка на доску")
	}
	if !strings.Contains(raw, "/") && !strings.Contains(raw, ".") {
		return Board{Service: Trello, Host: trelloHost, ID: raw, BaseURL: "https://trello.com"}, nil
	}
	u, err := url.Parse(withScheme(raw))
	if err != nil {
		return Board{}, err
	}
	host := strings.ToLower(u.Host)
	if isTrelloHost(host) {
		id := raw
		if m := reTrelloBoard.FindStringSubmatch(u.Path); m != nil {
			id = m[1]
		}
		return Board{Service: Trello, Host: trelloHost, ID: id, BaseURL: "https://trello.com"}, nil
	}
	m := reXYBoard.FindStringSubmatch(u.Path)
	if m == nil {
		return Board{}, fmt.Errorf("не понял, какая доска: %s", raw)
	}
	return Board{Service: XY, Host: host, ID: m[1], BaseURL: u.Scheme + "://" + u.Host}, nil
}

// ServiceHost is the key a token is stored under: every Trello link collapses to
// one, and each xy deployment keeps its own.
func ServiceHost(serviceURL string) string {
	u, err := url.Parse(withScheme(serviceURL))
	if err != nil || u.Host == "" {
		return trelloHost
	}
	host := strings.ToLower(u.Host)
	if isTrelloHost(host) {
		return trelloHost
	}
	return host
}

func isTrelloHost(host string) bool {
	return host == trelloHost || host == "www.trello.com" || strings.HasSuffix(host, ".trello.com")
}

func withScheme(s string) string {
	if strings.Contains(s, "://") {
		return s
	}
	return "https://" + s
}

// ── the token store: ~/.chgksuite/.board_tokens.toml ──

func suiteDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".chgksuite")
	return dir, os.MkdirAll(dir, 0o755)
}

func tokensPath() (string, error) {
	dir, err := suiteDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".board_tokens.toml"), nil
}

// TokenFor returns the token saved for a host, migrating a legacy
// .trello_token on the way, as chgksuite does.
func TokenFor(host string) (string, error) {
	tokens, err := loadTokens()
	if err != nil {
		return "", err
	}
	return tokens[host], nil
}

// SetTokenFor saves a token for a host.
func SetTokenFor(host, token string) error {
	tokens, err := loadTokens()
	if err != nil {
		return err
	}
	tokens[host] = token
	path, err := tokensPath()
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, h := range sortedKeys(tokens) {
		b.WriteString("[[tokens]]\nhost = " + tomlString(h) + "\ntoken = " + tomlString(tokens[h]) + "\n\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func loadTokens() (map[string]string, error) {
	if err := migrateLegacyTrelloToken(); err != nil {
		return nil, err
	}
	path, err := tokensPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}, nil //nolint:nilerr // no file is an empty store
	}
	out := map[string]string{}
	host := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "host":
			host = unquote(strings.TrimSpace(value))
		case "token":
			if host != "" {
				out[host] = unquote(strings.TrimSpace(value))
			}
		}
	}
	return out, nil
}

// migrateLegacyTrelloToken folds an old ~/.chgksuite/.trello_token into the
// store, once, and deletes it — chgksuite's own migration.
func migrateLegacyTrelloToken() error {
	dir, err := suiteDir()
	if err != nil {
		return err
	}
	legacy := filepath.Join(dir, ".trello_token")
	raw, err := os.ReadFile(legacy)
	if err != nil {
		return nil //nolint:nilerr // nothing to migrate
	}
	token := strings.TrimSpace(string(raw))
	path, _ := tokensPath()
	if _, err := os.Stat(path); err != nil && token != "" {
		if err := os.WriteFile(path,
			[]byte("[[tokens]]\nhost = "+tomlString(trelloHost)+"\ntoken = "+tomlString(token)+"\n\n"), 0o600); err != nil {
			return err
		}
	}
	return os.Remove(legacy)
}

// ── per-folder board_metadata.toml ──

// Metadata is what a synchronised folder remembers: which board it is, and (for
// xy) the passphrase that opens it.
type Metadata struct {
	BoardURL   string
	Passphrase string
}

func metadataPath(folder string) string { return filepath.Join(folder, "board_metadata.toml") }

// ReadMetadata reads a folder's board_metadata.toml, migrating a legacy
// .board_id first. ok is false when the folder has neither.
func ReadMetadata(folder string) (Metadata, bool, error) {
	if err := migrateLegacyBoardID(folder); err != nil {
		return Metadata{}, false, err
	}
	raw, err := os.ReadFile(metadataPath(folder))
	if err != nil {
		return Metadata{}, false, nil //nolint:nilerr // an unsynchronised folder
	}
	var m Metadata
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "board_url":
			m.BoardURL = unquote(strings.TrimSpace(value))
		case "passphrase":
			m.Passphrase = unquote(strings.TrimSpace(value))
		}
	}
	return m, m.BoardURL != "", nil
}

// WriteMetadata records which board a folder follows.
func WriteMetadata(folder string, m Metadata) error {
	b := "board_url = " + tomlString(m.BoardURL) + "\n"
	if m.Passphrase != "" {
		b += "passphrase = " + tomlString(m.Passphrase) + "\n"
	}
	return os.WriteFile(metadataPath(folder), []byte(b), 0o600)
}

func migrateLegacyBoardID(folder string) error {
	legacy := filepath.Join(folder, ".board_id")
	raw, err := os.ReadFile(legacy)
	if err != nil {
		return nil //nolint:nilerr // nothing to migrate
	}
	if _, err := os.Stat(metadataPath(folder)); err != nil {
		if id := strings.TrimSpace(string(raw)); id != "" {
			if err := WriteMetadata(folder, Metadata{BoardURL: "https://trello.com/b/" + id}); err != nil {
				return err
			}
		}
	}
	return os.Remove(legacy)
}

func tomlString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) + `"`
}

func unquote(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\n`, "\n").Replace(v[1 : len(v)-1])
	}
	return v
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
