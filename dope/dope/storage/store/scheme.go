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
	Questions         int             `json:"questions,omitempty"`
	Participants      []string        `json:"participants,omitempty"`
	Stickers          json.RawMessage `json:"stickers,omitempty"`
	Seeding           *SchemeSeeding  `json:"seeding,omitempty"`
}

// SchemeSeeding is the [init] declaration compiled from the scheme DSL: where
// the seeding comes from and how the source's metrics are ordered. It resolves
// only when the host presses «Import seed» — never on its own.
type SchemeSeeding struct {
	Source  string            `json:"source"`
	Sort    []SchemeSortRule  `json:"sort,omitempty"`
	Players *SchemePlayerSeed `json:"players,omitempty"`
	// Division keeps the seed to the teams carrying this Flag, or, with a
	// leading minus, to the teams not carrying it (ADR-0020).
	Division string `json:"division,omitempty"`
}

// SchemePlayerSeed is a seed composed over a Participant's players rather
// than over its own past results — what a team format needs, where a team
// is three people who played the fest's other games for three other teams.
// Games are the source Games in order, so a player rule reads place1, place2…
// by that order; Player defines a metric per player; Seed folds one of those
// over the Participant's players with mean / min / max / sum.
type SchemePlayerSeed struct {
	Games  []string          `json:"games"`
	Player map[string]string `json:"player,omitempty"`
	Seed   map[string]string `json:"seed,omitempty"`
}

// SchemeSortRule is a SortRule as a scheme writes it — the same key.
type SchemeSortRule = SortRule

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
	Kind      string          `json:"kind,omitempty"` // registered StageKind ('rr', …); empty = stage_type
	Slug      string          `json:"slug,omitempty"` // the Block's readable URL handle for synthetic tabs
	Position  int             `json:"position"`
	Grain     SchemeGrain     `json:"grain"`
	Matches   []SchemeMatch   `json:"matches"`
	Teams     []SchemeSlot    `json:"teams"`
	Bands     []int           `json:"bands,omitempty"` // per Teams entry: how many Losses it carries
	Sources   []string        `json:"sources"`
	Sort      json.RawMessage `json:"sort"`
	Config    json.RawMessage `json:"config"`
	Layout    json.RawMessage `json:"layout"`
	// Auto marks a reseed the resolver calculates on its own as soon as its
	// source Matches are finished: a Swiss pool, whose order is the seed and
	// leaves the host nothing to judge.
	Auto bool `json:"auto,omitempty"`
	// SeedFrom names the stage whose standings rank stands for a
	// Participant's seed when this stage ranks: a Swiss Block's entry order is
	// the qualifier's table, not the Game's entrant numbers.
	SeedFrom string `json:"seedFrom,omitempty"`
}

// SchemeGrain says where a stage sits in its Game. A stage row is a Wave — the
// finest grain the schedule has — so it names the Block it expands, the Group
// it ranks (round-robin only) and which turn at the tables it is. The remaining
// coordinate, the Round, lives on the Match: a Group plays all its rounds at one
// table, so one stage spans several Rounds and only a match knows which.
type SchemeGrain struct {
	Block string `json:"block,omitempty"`
	Wave  int    `json:"wave,omitempty"`
	Group string `json:"group,omitempty"`
}

// Normalized is what every writer must store. A stage that knows its Block is
// at least the first wave, so wave 0 there means "nobody said", not "wave
// zero" — and a scheme unmarshalled from JSON written before grain existed has
// no wave at all. Without this, the same tournament compiled from DSL and
// imported from JSON lands on different coordinates.
//
// A stage with no Block is unknown throughout, and stays that way: guessing a
// coordinate is the habit the grain columns exist to end.
func (g SchemeGrain) Normalized() SchemeGrain {
	if g.Block != "" && g.Wave == 0 {
		g.Wave = 1
	}
	return g
}

type SchemeMatch struct {
	Code             string       `json:"code"`
	Title            string       `json:"title"`
	Letter           string       `json:"letter,omitempty"` // The match's letter, dealt at compile time; "" for a match that has none
	Venue            int          `json:"venue"`
	BlockRound       int          `json:"round,omitempty"` // 1-based Round within the Block
	Wave             int          `json:"wave,omitempty"`  // 1-based wave, set where the stage spans several
	ParticipantCount int          `json:"participantCount"`
	Slots            []SchemeSlot `json:"slots"`
}

type SchemeSlot struct {
	Seed        *SchemeSeedRef      `json:"seed,omitempty"`
	FromMatch   *SchemeFromMatchRef `json:"fromMatch,omitempty"`
	Reseed      *SchemeReseedRef    `json:"reseed,omitempty"`
	Draw        *SchemeDraw         `json:"draw,omitempty"`
	Team        *SchemeTeamRef      `json:"team,omitempty"`
	Placeholder string              `json:"placeholder,omitempty"`
	Label       string              `json:"label,omitempty"`
}

// SchemeDraw is a Slot no result implies: the host seats it on the day, out of
// the places the Kind names as its candidates (CONTEXT.md, «Draw»). The
// resolver never fills it — that is the whole distinction between a Draw and a
// derived seating — so it is stored as a placeholder whose ref carries this,
// and the host's draw is written straight onto the slot.
type SchemeDraw struct {
	Code       string               `json:"code"`
	Candidates []SchemeFromMatchRef `json:"candidates,omitempty"`
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
