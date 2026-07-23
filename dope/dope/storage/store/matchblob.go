package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// The per-match Protocol state blob (matches.state_json, ADR-0002). Team
// sections are keyed by decimal team id — never by slot order — so reseeds
// can't reshuffle state and journal patches address a stable path. Theme
// players are stored as player ids; display names resolve at load time.
// Places are NOT here: they live in match_results (with place_override) as
// the Structure-facing output.
//
// Every mutation records a BlobOp; MutateMatchBlobTx returns them so the
// caller can journal the edit semantically (OpMatchPatch) instead of the row
// trigger capturing the whole blob.

// BlobOp is one recorded pointer operation on a match blob — the unit of the
// OpMatchPatch journal record. Kinds: "set", "remove", "ensure" (pad a theme
// list so the index exists, never overwriting).
type BlobOp struct {
	Kind  string `json:"k"`
	Path  string `json:"p"`
	Value any    `json:"v,omitempty"`
}

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

// MatchBlob is the decoded matches.state_json document plus the ops recorded
// by mutations since parse.
type MatchBlob struct {
	Teams map[string]*TeamBlob `json:"teams,omitempty"`

	Ops []BlobOp `json:"-"`
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

// JSON encodes the blob for storage in canonical form (sorted object keys, via
// a map round-trip), so a blob written live is byte-identical to the same blob
// reconstructed by journal replay.
func (b *MatchBlob) JSON() (string, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	var doc any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(canonical), nil
}

func (b *MatchBlob) record(kind, path string, value any) {
	b.Ops = append(b.Ops, BlobOp{Kind: kind, Path: path, Value: value})
}

func teamKey(teamID int64) string { return strconv.FormatInt(teamID, 10) }

// Team returns the team's section, creating it on first touch. Reading access
// only — mutations go through the MatchBlob methods below so ops record.
func (b *MatchBlob) Team(teamID int64) *TeamBlob {
	key := teamKey(teamID)
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

func kindSegment(kind string) string {
	if kind == "shootout" {
		return "shootoutThemes"
	}
	return "themes"
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

// SetAnswer sets one answer mark (normalised) on a team's theme, growing the
// grid as needed.
func (b *MatchBlob) SetAnswer(teamID int64, kind string, themeIndex, answerIndex int, mark string) {
	if answerIndex < 0 || answerIndex >= len(QuestionValues) {
		return
	}
	normalized := NormalizeMark(mark)
	b.Team(teamID).theme(kind, themeIndex).Answers[answerIndex] = normalized
	b.record("set",
		"/teams/"+teamKey(teamID)+"/"+kindSegment(kind)+"/"+strconv.Itoa(themeIndex)+"/answers/"+strconv.Itoa(answerIndex),
		normalized)
}

// SetPlayer assigns the fielded player (by id, 0 clears) on a team's theme.
func (b *MatchBlob) SetPlayer(teamID int64, kind string, themeIndex int, playerID int64) {
	b.Team(teamID).theme(kind, themeIndex).Player = playerID
	path := "/teams/" + teamKey(teamID) + "/" + kindSegment(kind) + "/" + strconv.Itoa(themeIndex) + "/player"
	if playerID == 0 {
		b.record("remove", path, nil)
	} else {
		b.record("set", path, playerID)
	}
}

// EnsureTheme guarantees a team's theme exists (padding the grid up to its
// index) without setting anything on it.
func (b *MatchBlob) EnsureTheme(teamID int64, kind string, themeIndex int) {
	b.Team(teamID).theme(kind, themeIndex)
	b.record("ensure",
		"/teams/"+teamKey(teamID)+"/"+kindSegment(kind)+"/"+strconv.Itoa(themeIndex), nil)
}

// RemoveTeam drops a team's whole section (a team that lost its seat).
func (b *MatchBlob) RemoveTeam(key string) {
	if _, ok := b.Teams[key]; !ok {
		return
	}
	delete(b.Teams, key)
	b.record("remove", "/teams/"+key, nil)
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
		b.EnsureTheme(id, "shootout", index)
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
	for key, section := range b.Teams {
		if len(section.ShootoutThemes) == last {
			section.ShootoutThemes = section.ShootoutThemes[:last-1]
			b.record("remove", "/teams/"+key+"/shootoutThemes/"+strconv.Itoa(last-1), nil)
		}
	}
	return nil
}

// MutateMatchBlobTx loads a match's state blob, applies fn, persists the
// result, and returns the recorded ops — the single write path for per-match
// Protocol state. The caller journals the returned ops (OpMatchPatch); the
// matches row trigger deliberately ignores state_json.
func MutateMatchBlobTx(ctx context.Context, tx *sql.Tx, matchID int64, fn func(*MatchBlob) error) ([]BlobOp, error) {
	var raw string
	if err := tx.QueryRowContext(ctx, `select state_json from matches where id = ?`, matchID).Scan(&raw); err != nil {
		return nil, err
	}
	blob, err := ParseMatchBlob(raw)
	if err != nil {
		return nil, err
	}
	if err := fn(&blob); err != nil {
		return nil, err
	}
	encoded, err := blob.JSON()
	if err != nil {
		return nil, err
	}
	if encoded == raw {
		return nil, nil
	}
	if _, err := tx.ExecContext(ctx, `update matches set state_json = ? where id = ?`, encoded, matchID); err != nil {
		return nil, err
	}
	return blob.Ops, nil
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
