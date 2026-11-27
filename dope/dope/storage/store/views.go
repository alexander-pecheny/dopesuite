package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

// Match view and state shapes. *State types mirror the persisted match state;
// *View types are the scored, client-facing projections the API serves and the
// SSE layer broadcasts. They are pure data (no DB/server dependency), so they
// live in the store leaf as the shared persistence/view vocabulary.

// ThemeEntry is one theme's seating and raw answer marks (persisted state).
// Players names whoever the team sent to the theme — one in EK, up to three in
// Erudit-Sextet — in no meaningful order.
type ThemeEntry struct {
	Players []string  `json:"players"`
	Answers [5]string `json:"answers"`
}

// RosterMember is one roster entry. The id travels to the client because blob
// paths address theme players by id, so an editor must send an id, never a name
// for the server to resolve (ADR-0005).
type RosterMember struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// RosterOf builds an id-less roster from bare names, for the demo state and
// fixtures that have no players table behind them.
func RosterOf(names ...string) []RosterMember {
	roster := make([]RosterMember, len(names))
	for i, name := range names {
		roster[i] = RosterMember{Name: name}
	}
	return roster
}

// HasRosterName reports whether roster lists a member by that display name.
func HasRosterName(roster []RosterMember, name string) bool {
	for _, member := range roster {
		if member.Name == name {
			return true
		}
	}
	return false
}

// ParticipantState is one Participant's persisted match state.
type ParticipantState struct {
	ID             int64          `json:"id,omitempty"`
	Name           string         `json:"name"`
	Roster         []RosterMember `json:"roster"`
	Themes         []ThemeEntry   `json:"themes"`
	ShootoutThemes []ThemeEntry   `json:"shootoutThemes,omitempty"`
	Tiebreak       int            `json:"tiebreak"`
	Place          float64        `json:"place"`
	// Total and Plus are what the scorer wrote to match_results for a
	// Protocol that keeps its own document: read for the view, never stored
	// here, since that document is the record.
	Total int `json:"-"`
	Plus  int `json:"-"`
}

// MatchState is the persisted state of a single match.
type MatchState struct {
	Title        string             `json:"title"`
	Finished     bool               `json:"finished"`
	Revision     int64              `json:"revision"`
	UpdatedAt    time.Time          `json:"updatedAt"`
	Participants []ParticipantState `json:"participants"`
}

// ThemeView is a scored theme row (the seating and raw marks plus the computed
// score).
type ThemeView struct {
	Players []string  `json:"players"`
	Answers [5]string `json:"answers"`
	Score   int       `json:"score"`
}

// ParticipantView is a Participant's scored, client-facing projection.
type ParticipantView struct {
	ID             int64          `json:"id,omitempty"`
	Name           string         `json:"name"`
	Roster         []RosterMember `json:"roster"`
	Themes         []ThemeView    `json:"themes"`
	ShootoutThemes []ThemeView    `json:"shootoutThemes"`
	Total          int            `json:"total"`
	Place          float64        `json:"place"`
	Plus           int            `json:"plus"`
	ShootoutTotal  int            `json:"shootoutTotal"`
	Tiebreak       int            `json:"tiebreak"`
	CorrectCounts  [5]int         `json:"correctCounts"`
	WrongCounts    [5]int         `json:"wrongCounts"`
}

// StandingView is one row of a match's standings.
type StandingView struct {
	Name     string  `json:"name"`
	Place    float64 `json:"place"`
	Total    int     `json:"total"`
	Plus     int     `json:"plus"`
	Tiebreak int     `json:"tiebreak"`
}

// VenueView is one of a game's venues (ADR-0037): its number, the room it is
// in (Room 0 and Title empty for none yet), and its hosts. On a bout, Hosts
// is who hosts that bout (its own, else the venue's) and OwnHosts its own;
// in a game's venue list, Bouts is how many of its bouts sit there.
type VenueView struct {
	Number   int    `json:"number"`
	Room     int64  `json:"room,omitempty"`
	Title    string `json:"title"`
	Hosts    string `json:"hosts,omitempty"`
	OwnHosts string `json:"ownHosts,omitempty"`
	Bouts    int    `json:"bouts,omitempty"`
}

// BoutVenueJoin is the join that puts a bout's venue on a query over matches
// m: its game's map line gv and the room r.
const BoutVenueJoin = `
left join game_venues gv on gv.game_id = m.game_id and gv.number = m.venue
left join rooms r on r.id = gv.room_id`

// BoutVenueColumns are the columns ScanBoutVenue reads, after BoutVenueJoin.
const BoutVenueColumns = `m.venue, coalesce(gv.room_id, 0), coalesce(r.title, ''), coalesce(gv.hosts, ''), m.hosts`

// BoutVenue is a scanned bout venue: none when the bout sits at no number.
type BoutVenue struct {
	Number     sql.NullInt64
	Room       int64
	Title      string
	Hosts, Own string
}

// Targets are BoutVenue's scan destinations, in BoutVenueColumns' order.
func (b *BoutVenue) Targets() []any {
	return []any{&b.Number, &b.Room, &b.Title, &b.Hosts, &b.Own}
}

// View is the bout's venue, nil for none.
func (b BoutVenue) View() *VenueView {
	if !b.Number.Valid {
		return nil
	}
	hosts := b.Hosts
	if b.Own != "" {
		hosts = b.Own
	}
	return &VenueView{Number: int(b.Number.Int64), Room: b.Room, Title: b.Title, Hosts: hosts, OwnHosts: b.Own}
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
	GameType          string      `json:"gameType,omitempty"`
	Revision          int64       `json:"revision"`
	UpdatedAt         string      `json:"updatedAt"`
	SchemaJSON        string      `json:"schemaJson,omitempty"`
	QuestionValues    [5]int      `json:"questionValues"`
	RegularThemeCount int         `json:"regularThemeCount"`
	Venues            []VenueView `json:"venues"`
	Stages            []StageView `json:"stages"`
	// GridWidth is the Game's grid column width in pixels, 0 for the default.
	GridWidth int `json:"gridWidth,omitempty"`
	// HiddenDivisions are the Flags whose divisions this game does not show.
	HiddenDivisions []string `json:"hiddenDivisions,omitempty"`
	// OutsideDivisions are the Flags whose teams this game ranks out of competition:
	// below everyone else in the whole field, with no place there.
	OutsideDivisions []string `json:"outsideDivisions,omitempty"`
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
	// Standings is a ranking Kind's own table — a Group's place against team.
	// It is the same shape and the same source as a reseed's entries; only the
	// meaning differs, so the game grid can draw a Group as the sheets do.
	Standings []ReseedEntryView `json:"standings,omitempty"`
	// Kind is the stage's Kind code; Sort the Metrics its Ranker ranked by, in
	// order — the columns a table of Standings (or ReseedEntries) shows.
	Kind string     `json:"kind,omitempty"`
	Sort []SortRule `json:"sort,omitempty"`
	// Grain says where the stage sits in its Game — the Block, the Group and
	// the Wave — so a page groups stages by what the compiler wrote, never by
	// what a code looks like.
	Grain *SchemeGrain `json:"grain,omitempty"`
}

// SortRule is one key of a Ranker's order: the Metric and its direction.
type SortRule struct {
	Metric string `json:"metric"`
	Dir    string `json:"dir"`
}

type ReseedEntryView struct {
	Rank          int             `json:"rank"`
	ParticipantID int64           `json:"participantID"`
	Name          string          `json:"name"`
	Metrics       json.RawMessage `json:"metrics,omitempty"`
}

type FestMatchView struct {
	Code             string     `json:"code"`
	Title            string     `json:"title"`
	Letter           string     `json:"letter,omitempty"`
	Position         int        `json:"position"`
	ParticipantCount int        `json:"participantCount"`
	Status           string     `json:"status"`
	Revision         int64      `json:"revision"`
	Venue            *VenueView `json:"venue,omitempty"`
	// StartsAt is when the bout starts, as the host typed it ("10:30"), or "".
	StartsAt     string                    `json:"startsAt,omitempty"`
	Participants []MatchParticipantSummary `json:"participants"`
	// ID and GameID are the bout's row and its Game's, for the readers that
	// fill in what its seats may hold (domain/festview, the Draw options).
	ID     int64 `json:"-"`
	GameID int64 `json:"-"`
}

type MatchParticipantSummary struct {
	Name       string `json:"name"`
	Source     string `json:"source,omitempty"`
	SourceType string `json:"sourceType,omitempty"`
	// Seed is the seed number of the team a seed's slot seats, 0 for any other
	// slot and for a seed's slot nobody sits in yet: the grid draws it before
	// the name.
	Seed     int     `json:"seed,omitempty"`
	Place    float64 `json:"place"`
	Total    int     `json:"total"`
	Plus     int     `json:"plus"`
	Tiebreak int     `json:"tiebreak"`
	// Draw is set on a seat the host fills by lot: who may be seated there,
	// and who already is. The candidates are resolved here rather than on the
	// page, because the page has no Participant ids to send back (ADR-0011).
	Draw *DrawSlotView `json:"draw,omitempty"`
}

// DrawSlotView is a Draw Slot as the grid's panel reads it: the slot's own
// code, the Participants it may be filled from — empty until every source
// Match is finished — and the one sitting in it now.
type DrawSlotView struct {
	Code       string              `json:"code"`
	Seated     int64               `json:"seated,omitempty"`
	Apart      bool                `json:"apart,omitempty"`
	Candidates []DrawCandidateView `json:"candidates,omitempty"`
	// Substitutes are whom an admin may seat instead when a drawn team drops
	// out: the teams of the same tables that went through on no place. They
	// are listed only once Candidates are.
	Substitutes []DrawCandidateView `json:"substitutes,omitempty"`
	// Draw is the Slot's declared Draw, which resolver.DrawOptions reads.
	Draw *SchemeDraw `json:"-"`
}

type DrawCandidateView struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Source is the stage or Match the candidate's place is in: what a draw
	// with Apart set keeps out of one Match.
	Source string `json:"source,omitempty"`
}

// MatchView is the scored, client-facing projection of a match.
type MatchView struct {
	Title          string     `json:"title"`
	Code           string     `json:"code,omitempty"`
	StageCode      string     `json:"stageCode,omitempty"`
	StageTitle     string     `json:"stageTitle,omitempty"`
	Venue          *VenueView `json:"venue,omitempty"`
	StartsAt       string     `json:"startsAt,omitempty"`
	Finished       bool       `json:"finished"`
	Revision       int64      `json:"revision"`
	UpdatedAt      string     `json:"updatedAt"`
	QuestionValues [5]int     `json:"questionValues"`
	// Players is how many of a team may sit on one theme of this match — one
	// in EK, three in Erudit-Sextet unless the Block says otherwise. The page
	// draws a native select at one and a seat picker above it.
	Players      int               `json:"players,omitempty"`
	Participants []ParticipantView `json:"participants"`
	Standings    []StandingView    `json:"standings"`
	// State carries the match's Protocol document verbatim; the per-protocol
	// renderer owns its shape. EK's is also projected into Participants.
	State json.RawMessage `json:"state,omitempty"`
	// Seq is the match scope's current SSE sequence. GET responses carry the
	// seq at fetch time, and mutating responses (update/finish/venue) carry the
	// seq their own broadcast assigned — so the editor that issued the edit can
	// keep its locally-applied view in lockstep with the delta it will also
	// receive over SSE and chain onto subsequent deltas. It is never set on
	// broadcast payloads themselves (so the delta diff ignores it).
	Seq uint64 `json:"seq,omitempty"`
}

// ParseHiddenDivisions reads games.hidden_divisions (or outside_divisions): a
// JSON list of Flag short names, empty or unreadable as none.
func ParseHiddenDivisions(raw string) []string {
	var out []string
	if raw == "" || json.Unmarshal([]byte(raw), &out) != nil {
		return nil
	}
	return out
}

// HiddenDivisionsValue is what games.hidden_divisions (or outside_divisions)
// holds for a list of Flag short names: its JSON, or null for none.
func HiddenDivisionsValue(hidden []string) any {
	if len(hidden) == 0 {
		return nil
	}
	raw, err := json.Marshal(hidden)
	if err != nil {
		return nil
	}
	return string(raw)
}
