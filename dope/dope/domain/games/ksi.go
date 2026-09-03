package games

import (
	"encoding/json"
	"fmt"
	"strings"
)

// KSI (team jeopardy) pure domain logic.
//
// The persisted KSI state JSON is structured as a list of participant teams and
// a list of themes, each theme holding a per-player × per-question grid of
// answer marks. The shapes below mirror that layout; they are shared by the
// xlsx export and any server-side scoring so the paths can't drift.

// KSIParticipant is one row of a KSI game's participants list. Number is the
// team's universal identity (the join key for the answer-grid remap); Name is
// shown in the UI. Stored as objects [{number,name}]; legacy states stored a
// bare name array and are read tolerantly by the server's parseKSIParticipants.
type KSIParticipant struct {
	Number int    `json:"number"`
	Name   string `json:"name"`
}

// KSIDeclinedKey is the key under which a participant's "declined to play" flag
// is stored: by number when numbered, else by lowercased name.
func KSIDeclinedKey(number int, name string) string {
	if number > 0 {
		return fmt.Sprintf("n%d", number)
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	return "s" + name
}

// KSIParticipantDeclined reports whether participant p is marked as having
// declined to play, per the declined map.
func KSIParticipantDeclined(declined map[string]bool, p KSIParticipant) bool {
	if len(declined) == 0 {
		return false
	}
	key := KSIDeclinedKey(p.Number, p.Name)
	return key != "" && declined[key]
}

// KSIEmptyGameJSON builds the pristine scheme/state for a KSI game. Shared by
// game creation and the clear-to-pristine path.
func KSIEmptyGameJSON(slug, title string, themesCount int) ([]byte, []byte) {
	return ksiEmptyGameJSON(slug, title, themesCount, nil)
}

// KSIStickersEmptyGameJSON builds a pristine KSI game that carries a sticker
// configuration in its scheme — the "KSI with stickers" variant. The state is
// identical to a plain KSI game (per-team sticker choices live on the free-form
// state blob and are filled in live); only the scheme gains the `stickers` key.
func KSIStickersEmptyGameJSON(slug, title string, themesCount int, stickers json.RawMessage) ([]byte, []byte) {
	return ksiEmptyGameJSON(slug, title, themesCount, stickers)
}

func ksiEmptyGameJSON(slug, title string, themesCount int, stickers json.RawMessage) ([]byte, []byte) {
	themes := make([]map[string]any, themesCount)
	for i := range themes {
		themes[i] = map[string]any{"answers": [][]string{}}
	}
	scheme := map[string]any{
		"schemaVersion": 2,
		"slug":          slug,
		"title":         title,
		"gameType":      KSI,
		"participants":  []string{},
		"themes":        themesCount,
	}
	if len(stickers) > 0 {
		scheme["stickers"] = stickers
	}
	schemeJSON := []byte(mustJSON(scheme))
	stateJSON := []byte(mustJSON(map[string]any{
		"participants": []string{},
		"themes":       themes,
		"finished":     false,
	}))
	return schemeJSON, stateJSON
}

// Sticker type identifiers for the "KSI with stickers" variant. Each answer
// sheet (one team's sheet for one theme) carries exactly one sticker; the
// sticker chosen for a (team, theme) decides how that theme's value is scored.
const (
	KSIStickerNeutral    = "neutral"    // scores like a regular KSI theme
	KSIStickerX2         = "x2"         // both right and wrong values are doubled
	KSIStickerNoWrong    = "nowrong"    // wrong answers score 0 instead of -nominal
	KSIStickerEmptyWrong = "emptywrong" // empty answers score -nominal, like wrong
)

// KSIStickerType is one configured sticker: its id (fixed meaning), display
// label, UI highlight colour, and Max — the most a single team may use (nil =
// unlimited, as for the neutral sticker).
type KSIStickerType struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Color string `json:"color"`
	Max   *int   `json:"max,omitempty"`
}

// KSIStickerConfig is the scheme-level `stickers` block of a stickers game.
type KSIStickerConfig struct {
	Types []KSIStickerType `json:"types"`
}

// KSIStickerMarkValue returns the signed point value of a single answer mark of
// nominal worth `value`, scored under the given sticker. Callers pass canonical
// marks ("right"/"wrong"/anything-else = empty). An empty/unknown sticker id
// falls back to neutral scoring. This is the single source of truth shared by
// the xlsx export, the seed scoring and (mirrored) the si.js UI, so the paths
// can't drift.
func KSIStickerMarkValue(stickerID, mark string, value int) int {
	switch stickerID {
	case KSIStickerX2:
		switch mark {
		case "right":
			return 2 * value
		case "wrong":
			return -2 * value
		default:
			return 0
		}
	case KSIStickerNoWrong:
		if mark == "right" {
			return value
		}
		return 0
	case KSIStickerEmptyWrong:
		if mark == "right" {
			return value
		}
		return -value
	default: // neutral, and any unknown id
		switch mark {
		case "right":
			return value
		case "wrong":
			return -value
		default:
			return 0
		}
	}
}

// SIThemeCount is individual SI's usual match: eight themes. The regulation
// varies it by round (six early, twelve in the grand final), so a game's
// scheme overrides it — this is only the default.
const SIThemeCount = 8

// SIEmptyGameJSON builds the pristine scheme/state for an individual SI
// match: the same themes × participants grid as KSI, sized for the players
// at one table.
func SIEmptyGameJSON(slug, title string, themesCount, participants int) ([]byte, []byte) {
	if themesCount <= 0 {
		themesCount = SIThemeCount
	}
	seats := make([]map[string]any, participants)
	for i := range seats {
		seats[i] = map[string]any{"number": i + 1, "name": ""}
	}
	themes := make([]map[string]any, themesCount)
	for i := range themes {
		answers := make([][]string, participants)
		for p := range answers {
			answers[p] = make([]string, 5)
		}
		themes[i] = map[string]any{"answers": answers}
	}
	schemeJSON := []byte(mustJSON(map[string]any{
		"schemaVersion": 2,
		"slug":          slug,
		"title":         title,
		"gameType":      SI,
		"participants":  seats,
		"themes":        themesCount,
	}))
	stateJSON := []byte(mustJSON(map[string]any{
		"participants": seats,
		"themes":       themes,
		"finished":     false,
	}))
	return schemeJSON, stateJSON
}

// ParseKSIParticipants decodes a participants array tolerating both the current
// [{number,name}] shape and the legacy ["name", ...] shape (so existing game
// states keep loading during/after the migration).
func ParseKSIParticipants(raw json.RawMessage) []KSIParticipant {
	if len(raw) == 0 {
		return nil
	}
	var objs []KSIParticipant
	if err := json.Unmarshal(raw, &objs); err == nil {
		return objs
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err == nil {
		out := make([]KSIParticipant, len(names))
		for i, name := range names {
			out[i] = KSIParticipant{Name: name}
		}
		return out
	}
	return nil
}
