package store

import (
	"encoding/json"
	"strconv"
	"strings"
)

// The fest scheme is the persisted, JSON-authored description of a tournament's
// structure (venues, stages, matches, seed slots). These are pure data shapes
// parsed from games.scheme_json; they carry no DB or server dependency, so they
// belong in the store leaf alongside the rest of the persistence types.

type FestScheme struct {
	SchemaVersion     int             `json:"schemaVersion"`
	Slug              string          `json:"slug"`
	Title             string          `json:"title"`
	GameType          string          `json:"gameType"`
	QuestionValues    []int           `json:"questionValues"`
	RegularThemeCount int             `json:"regularThemeCount"`
	Venues            []SchemeVenue   `json:"venues"`
	Stages            []SchemeStage   `json:"stages"`
	Teams             []SchemeTeam    `json:"teams"`
	TourComp          json.RawMessage `json:"tourComp,omitempty"`
	NTeams            int             `json:"nTeams,omitempty"`
	Themes            int             `json:"themes,omitempty"`
	Participants      []string        `json:"participants,omitempty"`
	Stickers          json.RawMessage `json:"stickers,omitempty"`
}

type SchemeTeam struct {
	Name    string   `json:"name"`
	City    string   `json:"city"`
	Basket  int      `json:"basket"`
	Number  int      `json:"number"`
	Players []string `json:"players"`
}

type SchemeVenue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
}

type SchemeStage struct {
	Code      string          `json:"code"`
	Title     string          `json:"title"`
	StageType string          `json:"stage_type"`
	Position  int             `json:"position"`
	Matches   []SchemeMatch   `json:"matches"`
	Teams     []SchemeSlot    `json:"teams"`
	Sources   []string        `json:"sources"`
	Sort      json.RawMessage `json:"sort"`
	Config    json.RawMessage `json:"config"`
	Layout    json.RawMessage `json:"layout"`
}

type SchemeMatch struct {
	Code             string       `json:"code"`
	Title            string       `json:"title"`
	Venue            int          `json:"venue"`
	ParticipantCount int          `json:"participantCount"`
	Slots            []SchemeSlot `json:"slots"`
}

type SchemeSlot struct {
	Seed        *SchemeSeedRef      `json:"seed,omitempty"`
	FromMatch   *SchemeFromMatchRef `json:"fromMatch,omitempty"`
	Reseed      *SchemeReseedRef    `json:"reseed,omitempty"`
	Team        *SchemeTeamRef      `json:"team,omitempty"`
	Placeholder string              `json:"placeholder,omitempty"`
	Label       string              `json:"label,omitempty"`
}

type SchemeSeedRef struct {
	Basket   int `json:"basket,omitempty"`
	Number   int `json:"number,omitempty"`
	Position int `json:"position,omitempty"`
}

type SchemeFromMatchRef struct {
	Match string `json:"match"`
	Place int    `json:"place"`
}

type SchemeReseedRef struct {
	Stage string `json:"stage"`
	Rank  int    `json:"rank"`
}

type SchemeTeamRef struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	City    string   `json:"city"`
	Label   string   `json:"label"`
	Players []string `json:"players"`
}

// UnmarshalJSON accepts a slot written either as a bare string token
// ("seed-3" or a free placeholder) or as a full object.
func (slot *SchemeSlot) UnmarshalJSON(data []byte) error {
	var token string
	if err := json.Unmarshal(data, &token); err == nil {
		if number, ok := parseSeedToken(token); ok {
			slot.Seed = &SchemeSeedRef{Number: number}
			slot.Label = token
			return nil
		}
		slot.Placeholder = token
		slot.Label = token
		return nil
	}

	type schemeSlotAlias SchemeSlot
	var parsed schemeSlotAlias
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*slot = SchemeSlot(parsed)
	return nil
}

func parseSeedToken(token string) (int, bool) {
	token = strings.TrimSpace(token)
	rest, ok := strings.CutPrefix(token, "seed-")
	if !ok {
		return 0, false
	}
	number, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil || number <= 0 {
		return 0, false
	}
	return number, true
}
