package games

import (
	"fmt"
	"strings"
)

// KSI (командная своя игра) pure domain logic.
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
	themes := make([]map[string]any, themesCount)
	for i := range themes {
		themes[i] = map[string]any{"answers": [][]string{}}
	}
	schemeJSON := []byte(mustJSON(map[string]any{
		"schemaVersion": 2,
		"slug":          slug,
		"title":         title,
		"gameType":      KSI,
		"participants":  []string{},
		"themes":        themesCount,
	}))
	stateJSON := []byte(mustJSON(map[string]any{
		"participants": []string{},
		"themes":       themes,
		"finished":     false,
	}))
	return schemeJSON, stateJSON
}
