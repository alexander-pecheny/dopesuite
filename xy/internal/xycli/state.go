package xycli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

// State is what xy-cli remembers between commands: one instance, its API token,
// and the data key of every Board that has been unlocked. The data keys are the
// reason for 0600 and for ADR-0016 — an agent must decrypt with nobody at the
// keyboard, so the key that the browser keeps in IndexedDB is kept here in a
// file instead.
type State struct {
	URL    string             `json:"url"`
	Token  string             `json:"token"`
	Boards map[string]HeldKey `json:"boards,omitempty"`
	path   string             `json:"-"`
}

// HeldKey is one unlocked Board: its plaintext name (for --board by name) and
// its raw data key. The board id is the map key in the file; `id` carries it
// once a lookup has taken the entry out of the map.
type HeldKey struct {
	Name string `json:"name"`
	DK   string `json:"dk"` // base64 raw data key
	id   int64  `json:"-"`
}

// StatePath is the file State lives in: $XY_CLI_STATE, else
// $XDG_CONFIG_HOME/xy-cli/state.json, else ~/.config/xy-cli/state.json.
func StatePath() (string, error) {
	if p := os.Getenv("XY_CLI_STATE"); p != "" {
		return p, nil
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "xy-cli", "state.json"), nil
}

// LoadState reads the state file; a missing one is an empty state, not an error
// (that is what `login` is for).
func LoadState() (*State, error) {
	path, err := StatePath()
	if err != nil {
		return nil, err
	}
	st := &State{path: path, Boards: map[string]HeldKey{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, st); err != nil {
		return nil, err
	}
	st.path = path
	if st.Boards == nil {
		st.Boards = map[string]HeldKey{}
	}
	// XY_URL overrides the stored instance, so pointing at xytest is one env var.
	if u := os.Getenv("XY_URL"); u != "" {
		st.URL = u
	}
	return st, nil
}

// Save writes the state back, owner-readable only.
func (s *State) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(raw, '\n'), 0o600)
}

// Key returns the held data key of a board.
func (s *State) Key(boardID int64) (DataKey, bool) {
	held, ok := s.Boards[strconv.FormatInt(boardID, 10)]
	if !ok {
		return nil, false
	}
	dk, err := b64dec(held.DK)
	if err != nil {
		return nil, false
	}
	return dk, true
}

// Hold remembers a board's key; Forget drops it.
func (s *State) Hold(boardID int64, name string, dk DataKey) {
	s.Boards[strconv.FormatInt(boardID, 10)] = HeldKey{Name: name, DK: b64enc(dk)}
}

func (s *State) Forget(boardID int64) {
	delete(s.Boards, strconv.FormatInt(boardID, 10))
}
