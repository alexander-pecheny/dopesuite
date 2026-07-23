package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
)

// The per-match Protocol state blob (matches.state_json, ADR-0002). Team
// sections are keyed by decimal team id — never by slot order — so reseeds
// can't reshuffle state and journal patches address a stable path. Theme
// players are stored as player ids; display names resolve at load time.
// Places are NOT here: they live in match_results (with place_override) as
// the Structure-facing output.

// BlobTheme is one theme's state in a match blob: the fielded player (id,
// 0 = none) and the raw answer marks.
type BlobTheme struct {
	Player  int64     `json:"player,omitempty"`
	Answers [5]string `json:"answers"`
}

// TeamBlob is one team's section of a match blob.
type TeamBlob struct {
	Themes         []BlobTheme `json:"themes,omitempty"`
	ShootoutThemes []BlobTheme `json:"shootoutThemes,omitempty"`
}

// MatchBlob is the decoded matches.state_json document.
type MatchBlob struct {
	Teams map[string]*TeamBlob `json:"teams,omitempty"`
}

// ParseMatchBlob decodes a matches.state_json document (” and '{}' are the
// empty blob).
func ParseMatchBlob(raw string) (MatchBlob, error) {
	var blob MatchBlob
	if raw == "" {
		return blob, nil
	}
	if err := json.Unmarshal([]byte(raw), &blob); err != nil {
		return MatchBlob{}, err
	}
	return blob, nil
}

// JSON encodes the blob for storage.
func (b *MatchBlob) JSON() (string, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// Team returns the team's section, creating it on first touch.
func (b *MatchBlob) Team(teamID int64) *TeamBlob {
	key := strconv.FormatInt(teamID, 10)
	if b.Teams == nil {
		b.Teams = map[string]*TeamBlob{}
	}
	if section, ok := b.Teams[key]; ok {
		return section
	}
	section := &TeamBlob{}
	b.Teams[key] = section
	return section
}

func (t *TeamBlob) themes(kind string) *[]BlobTheme {
	if kind == "shootout" {
		return &t.ShootoutThemes
	}
	return &t.Themes
}

func (t *TeamBlob) theme(kind string, index int) *BlobTheme {
	list := t.themes(kind)
	for len(*list) <= index {
		*list = append(*list, BlobTheme{})
	}
	return &(*list)[index]
}

// SetAnswer sets one answer mark (normalised) on a theme, growing the grid as
// needed.
func (t *TeamBlob) SetAnswer(kind string, themeIndex, answerIndex int, mark string) {
	if answerIndex < 0 || answerIndex >= len(QuestionValues) {
		return
	}
	t.theme(kind, themeIndex).Answers[answerIndex] = NormalizeMark(mark)
}

// SetPlayer assigns the fielded player (by id, 0 clears) on a theme.
func (t *TeamBlob) SetPlayer(kind string, themeIndex int, playerID int64) {
	t.theme(kind, themeIndex).Player = playerID
}

// EnsureTheme guarantees the theme exists (padding the grid up to its index)
// without setting anything on it.
func (t *TeamBlob) EnsureTheme(kind string, themeIndex int) {
	t.theme(kind, themeIndex)
}

// AddShootoutTheme appends one shootout theme for every listed team and
// returns its index (teams stay in lockstep; existing sections pad).
func (b *MatchBlob) AddShootoutTheme(teamIDs []int64) int {
	index := 0
	for _, id := range teamIDs {
		if id == 0 {
			continue
		}
		if n := len(b.Team(id).ShootoutThemes); n > index {
			index = n
		}
	}
	for _, id := range teamIDs {
		if id == 0 {
			continue
		}
		b.Team(id).theme("shootout", index)
	}
	return index
}

// RemoveShootoutTheme drops the last shootout theme from every team section.
func (b *MatchBlob) RemoveShootoutTheme() error {
	last := 0
	for _, section := range b.Teams {
		if n := len(section.ShootoutThemes); n > last {
			last = n
		}
	}
	if last == 0 {
		return errors.New("no shootout themes to remove")
	}
	for _, section := range b.Teams {
		if len(section.ShootoutThemes) == last {
			section.ShootoutThemes = section.ShootoutThemes[:last-1]
		}
	}
	return nil
}

// MutateMatchBlobTx loads a match's state blob, applies fn, and persists the
// result — the single write path for per-match Protocol state.
func MutateMatchBlobTx(ctx context.Context, tx *sql.Tx, matchID int64, fn func(*MatchBlob) error) error {
	var raw string
	if err := tx.QueryRowContext(ctx, `select state_json from matches where id = ?`, matchID).Scan(&raw); err != nil {
		return err
	}
	blob, err := ParseMatchBlob(raw)
	if err != nil {
		return err
	}
	if err := fn(&blob); err != nil {
		return err
	}
	encoded, err := blob.JSON()
	if err != nil {
		return err
	}
	if encoded == raw {
		return nil
	}
	_, err = tx.ExecContext(ctx, `update matches set state_json = ? where id = ?`, encoded, matchID)
	return err
}

// TeamStateFromBlob projects one team's blob section into the legacy TeamState
// shape consumed by BuildView and the whole view layer: identity fields join
// in from the relational side, theme players resolve id → name via playerName,
// the grid pads to ThemeCount and marks normalise (NormalizeState runs later
// on the whole match and is idempotent over this).
func TeamStateFromBlob(section *TeamBlob, name string, roster []string, place float64, playerName func(int64) string) TeamState {
	team := TeamState{Name: name, Roster: roster, Place: place, Themes: make([]ThemeEntry, ThemeCount)}
	if section == nil {
		return team
	}
	project := func(themes []BlobTheme, out []ThemeEntry) []ThemeEntry {
		for i, theme := range themes {
			for len(out) <= i {
				out = append(out, ThemeEntry{})
			}
			if theme.Player != 0 && playerName != nil {
				out[i].Player = playerName(theme.Player)
			}
			for a, mark := range theme.Answers {
				out[i].Answers[a] = NormalizeMark(mark)
			}
		}
		return out
	}
	team.Themes = project(section.Themes, team.Themes)
	team.ShootoutThemes = project(section.ShootoutThemes, nil)
	return team
}
