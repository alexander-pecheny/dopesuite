package store

import (
	"encoding/json"
	"time"
)

// Match view and state shapes. *State types mirror the persisted match state;
// *View types are the scored, client-facing projections the API serves and the
// SSE layer broadcasts. They are pure data (no DB/server dependency), so they
// live in the store leaf as the shared persistence/view vocabulary.

// ThemeEntry is one player's raw answer marks for a theme (persisted state).
type ThemeEntry struct {
	Player  string    `json:"player"`
	Answers [5]string `json:"answers"`
}

// TeamState is one team's persisted match state.
type TeamState struct {
	Name           string       `json:"name"`
	Roster         []string     `json:"roster"`
	Themes         []ThemeEntry `json:"themes"`
	ShootoutThemes []ThemeEntry `json:"shootoutThemes,omitempty"`
	Tiebreak       int          `json:"tiebreak"`
	Place          float64      `json:"place"`
}

// MatchState is the persisted state of a single match.
type MatchState struct {
	Title     string      `json:"title"`
	Finished  bool        `json:"finished"`
	Revision  int64       `json:"revision"`
	UpdatedAt time.Time   `json:"updatedAt"`
	Teams     []TeamState `json:"teams"`
}

// ThemeView is a scored theme row (raw marks plus the computed score).
type ThemeView struct {
	Player  string    `json:"player"`
	Answers [5]string `json:"answers"`
	Score   int       `json:"score"`
}

// TeamView is a team's scored, client-facing projection.
type TeamView struct {
	Name           string      `json:"name"`
	Roster         []string    `json:"roster"`
	Themes         []ThemeView `json:"themes"`
	ShootoutThemes []ThemeView `json:"shootoutThemes"`
	Total          int         `json:"total"`
	Place          float64     `json:"place"`
	Plus           int         `json:"plus"`
	ShootoutTotal  int         `json:"shootoutTotal"`
	Tiebreak       int         `json:"tiebreak"`
	CorrectCounts  [5]int      `json:"correctCounts"`
	WrongCounts    [5]int      `json:"wrongCounts"`
}

// StandingView is one row of a match's standings.
type StandingView struct {
	Name     string  `json:"name"`
	Place    float64 `json:"place"`
	Total    int     `json:"total"`
	Plus     int     `json:"plus"`
	Tiebreak int     `json:"tiebreak"`
}

// VenueView is a match's venue (number + title).
type VenueView struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
}

// StageMatches is one stage's full match views (the bulk all-stages response
// shape, also consumed by the xlsx export).
type StageMatches struct {
	Code    string      `json:"code"`
	Matches []MatchView `json:"matches"`
}

// FestView is the fest-wide projection the public/host dashboards render: the
// fest header plus its venues and per-stage match grid. Cached per (fest, game)
// and broadcast on any change.
type FestView struct {
	Slug              string      `json:"slug"`
	Title             string      `json:"title"`
	GameName          string      `json:"gameName,omitempty"`
	Revision          int64       `json:"revision"`
	UpdatedAt         string      `json:"updatedAt"`
	SchemaJSON        string      `json:"schemaJson,omitempty"`
	QuestionValues    [5]int      `json:"questionValues"`
	RegularThemeCount int         `json:"regularThemeCount"`
	Venues            []VenueView `json:"venues"`
	Stages            []StageView `json:"stages"`
}

type StageView struct {
	Code          string            `json:"code"`
	Title         string            `json:"title"`
	Type          string            `json:"stage_type"`
	Position      int               `json:"position"`
	Status        string            `json:"status"`
	Config        json.RawMessage   `json:"config,omitempty"`
	ReseedReady   bool              `json:"reseedReady,omitempty"`
	ReseedPending []string          `json:"reseedPendingMatches,omitempty"`
	ReseedMessage string            `json:"reseedBlockedMessage,omitempty"`
	Matches       []FestMatchView   `json:"matches,omitempty"`
	ReseedEntries []ReseedEntryView `json:"reseedEntries,omitempty"`
}

type ReseedEntryView struct {
	Rank    int             `json:"rank"`
	TeamID  int64           `json:"teamID"`
	Name    string          `json:"name"`
	Metrics json.RawMessage `json:"metrics,omitempty"`
}

type FestMatchView struct {
	Code             string             `json:"code"`
	Title            string             `json:"title"`
	Position         int                `json:"position"`
	ParticipantCount int                `json:"participantCount"`
	Status           string             `json:"status"`
	Revision         int64              `json:"revision"`
	Venue            *VenueView         `json:"venue,omitempty"`
	Teams            []MatchTeamSummary `json:"teams"`
}

type MatchTeamSummary struct {
	Name       string  `json:"name"`
	Source     string  `json:"source,omitempty"`
	SourceType string  `json:"sourceType,omitempty"`
	Place      float64 `json:"place"`
	Total      int     `json:"total"`
	Plus       int     `json:"plus"`
	Tiebreak   int     `json:"tiebreak"`
}

// MatchView is the scored, client-facing projection of a match.
type MatchView struct {
	Title          string         `json:"title"`
	Code           string         `json:"code,omitempty"`
	StageCode      string         `json:"stageCode,omitempty"`
	StageTitle     string         `json:"stageTitle,omitempty"`
	Venue          *VenueView     `json:"venue,omitempty"`
	Finished       bool           `json:"finished"`
	Revision       int64          `json:"revision"`
	UpdatedAt      string         `json:"updatedAt"`
	QuestionValues [5]int         `json:"questionValues"`
	Teams          []TeamView     `json:"teams"`
	Standings      []StandingView `json:"standings"`
	// Seq is the match scope's current SSE sequence. GET responses carry the
	// seq at fetch time, and mutating responses (update/finish/venue) carry the
	// seq their own broadcast assigned — so the editor that issued the edit can
	// keep its locally-applied view in lockstep with the delta it will also
	// receive over SSE and chain onto subsequent deltas. It is never set on
	// broadcast payloads themselves (so the delta diff ignores it).
	Seq uint64 `json:"seq,omitempty"`
}
