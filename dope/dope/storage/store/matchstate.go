package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	dopestrings "dope/i18nstrings"
)

// ThemeCount is how many regular themes a match has when nothing says otherwise —
// EK's twelve. A Kind that plays a different number says so in its stage config
// (individual SI's groups play six, its play-off eight, its grand final twelve),
// and a match padded to twelve when it played six draws six empty columns
// nobody can fill.
const ThemeCount = 12

// QuestionValues is the EK/KSI per-answer point scale (lowest to highest).
var QuestionValues = [5]int{10, 20, 30, 40, 50}

// DBMatchState is a match's full state as loaded from the DB: the match header,
// its venue, the scored MatchState, and the per-slot team ids.
type DBMatchState struct {
	MatchID        int64
	GameID         int64
	GameType       string
	Code           string
	Title          string
	Status         string
	Revision       int64
	FestRevision   int64
	UpdatedAt      time.Time
	StageCode      string
	StageTitle     string
	Venue          *VenueView
	State          MatchState
	Blob           MatchBlob
	RawState       string // verbatim matches.state_json — the Protocol document for non-EK games
	ParticipantIDs []int64
	RosterSource   string
	// Themes is how many regular themes this match plays, from its stage. Zero
	// means the default — a match whose stage never said.
	Themes int
}

// teamBlobProtocols are the Protocols whose match state is the team-keyed
// blob (matchops/MatchBlob), which the loader projects into slots and every
// generic edit addresses by team; each registers itself (protocol.Register).
// A game with no type is the legacy EK fixture.
var teamBlobProtocols = map[string]bool{"": true}

func RegisterTeamBlob(code string) { teamBlobProtocols[code] = true }

func TeamBlobShaped(gameType string) bool { return teamBlobProtocols[gameType] }

// seatRosterProtocols are the Protocols whose matches name players and so
// need each seat's roster loaded — every team-blob one, and Troika, whose
// document records which of a team's three sat in which chair. Each registers
// itself (protocol.Register).
var seatRosterProtocols = map[string]bool{}

func RegisterSeatRoster(code string) { seatRosterProtocols[code] = true }

// SeatsPlayers reports whether a match of this Protocol names players in its
// seats, and therefore wants their roster on the view.
func SeatsPlayers(gameType string) bool {
	return TeamBlobShaped(gameType) || seatRosterProtocols[gameType]
}

// ProtocolState is the document the match's Protocol scores: the projected
// MatchState for a team-blob game, the raw document for the rest.
func (m DBMatchState) ProtocolState() json.RawMessage {
	if TeamBlobShaped(m.GameType) {
		state, _ := json.Marshal(m.State)
		return state
	}
	if m.RawState == "" {
		return json.RawMessage("{}")
	}
	return json.RawMessage(m.RawState)
}

// MatchViewFrom scores a loaded match into its client-facing view, joining the
// header fields BuildView doesn't see. A non-EK match carries its Protocol
// document verbatim in State plus light slot-occupant rows — the renderer owns
// the shape, the view only frames it.
func MatchViewFrom(match DBMatchState) MatchView {
	if !TeamBlobShaped(match.GameType) {
		teams := make([]ParticipantView, len(match.State.Participants))
		for i, team := range match.State.Participants {
			teams[i] = ParticipantView{ID: team.ID, Name: team.Name, Roster: team.Roster, Place: team.Place}
		}
		return MatchView{
			Title:        match.Title,
			Code:         match.Code,
			StageCode:    match.StageCode,
			StageTitle:   match.StageTitle,
			Venue:        match.Venue,
			Finished:     match.Status == "finished",
			Revision:     match.Revision,
			UpdatedAt:    match.UpdatedAt.Format(time.RFC3339),
			State:        json.RawMessage(match.RawState),
			Participants: teams,
		}
	}
	view := BuildView(match.State)
	view.Code = match.Code
	view.StageCode = match.StageCode
	view.StageTitle = match.StageTitle
	view.Venue = match.Venue
	return view
}

// ParseDBTime parses an RFC3339 timestamp, falling back to now on error.
func ParseDBTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Now()
	}
	return parsed
}

// JoinPlayerName joins first and last name into a trimmed display string.
func JoinPlayerName(firstName, lastName string) string {
	return strings.TrimSpace(strings.TrimSpace(firstName) + " " + strings.TrimSpace(lastName))
}

// NormalizeMark canonicalises an answer mark to "right"/"wrong"/"" tolerating
// the various keyboard inputs the client may send.
func NormalizeMark(mark string) string {
	switch strings.ToLower(strings.TrimSpace(mark)) {
	case "right", "q", "й", "1", "+":
		return "right"
	case "wrong", "w", "ц", "-1", "-", "−1", "−":
		return "wrong"
	default:
		return ""
	}
}

// NormalizeState is NormalizeStateTo at the default theme count.
func NormalizeState(state *MatchState) {
	NormalizeStateTo(state, ThemeCount)
}

// NormalizeStateTo fills defaults and pads/clamps each team's themes to a
// uniform shape, normalising every answer mark, so a freshly-loaded or
// hand-edited state is well-formed before scoring/serving. themes <= 0 means
// the default.
func NormalizeStateTo(state *MatchState, themes int) {
	if themes <= 0 {
		themes = ThemeCount
	}
	if state.Title == "" {
		state.Title = dopestrings.Default.Storage.Match.DefaultTitle()
	}
	if state.Revision == 0 {
		state.Revision = 1
	}
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now()
	}
	shootoutThemeCount := 0
	for i := range state.Participants {
		if len(state.Participants[i].ShootoutThemes) > shootoutThemeCount {
			shootoutThemeCount = len(state.Participants[i].ShootoutThemes)
		}
	}
	for i := range state.Participants {
		state.Participants[i].Tiebreak = 0
		if len(state.Participants[i].Themes) < themes {
			missing := themes - len(state.Participants[i].Themes)
			state.Participants[i].Themes = append(state.Participants[i].Themes, make([]ThemeEntry, missing)...)
		}
		if len(state.Participants[i].Themes) > themes {
			state.Participants[i].Themes = state.Participants[i].Themes[:themes]
		}
		for t := range state.Participants[i].Themes {
			for a := range state.Participants[i].Themes[t].Answers {
				state.Participants[i].Themes[t].Answers[a] = NormalizeMark(state.Participants[i].Themes[t].Answers[a])
			}
		}
		if len(state.Participants[i].ShootoutThemes) < shootoutThemeCount {
			missing := shootoutThemeCount - len(state.Participants[i].ShootoutThemes)
			state.Participants[i].ShootoutThemes = append(state.Participants[i].ShootoutThemes, make([]ThemeEntry, missing)...)
		}
		for t := range state.Participants[i].ShootoutThemes {
			for a := range state.Participants[i].ShootoutThemes[t].Answers {
				state.Participants[i].ShootoutThemes[t].Answers[a] = NormalizeMark(state.Participants[i].ShootoutThemes[t].Answers[a])
			}
		}
	}
}

// MatchSelector names the matches a loader reads; every field given narrows the
// set. A fest and a code is one match of a bracket; a fest, game and match id
// is one match exactly; a fest and game is the whole game, a stage code one
// Block of it.
type MatchSelector struct {
	FestID, GameID, MatchID int64
	Code, StageCode         string
}

func (sel MatchSelector) where() (string, []any) {
	var clauses []string
	var args []any
	add := func(clause string, arg any) {
		clauses = append(clauses, clause)
		args = append(args, arg)
	}
	if sel.FestID != 0 {
		add("m.fest_id = ?", sel.FestID)
	}
	if sel.GameID != 0 {
		add("m.game_id = ?", sel.GameID)
	}
	if sel.MatchID != 0 {
		add("m.id = ?", sel.MatchID)
	}
	if sel.Code != "" {
		add("m.code = ?", sel.Code)
	}
	if sel.StageCode != "" {
		add("s.code = ?", sel.StageCode)
	}
	if len(clauses) == 0 {
		return "1", nil
	}
	return strings.Join(clauses, " and "), args
}

// LoadDBMatchState loads a match by fest id and code.
func LoadDBMatchState(ctx context.Context, q Queryer, festID int64, code string) (DBMatchState, error) {
	return LoadMatchState(ctx, q, MatchSelector{FestID: festID, Code: code})
}

// LoadMatchState loads the first match the selector names, in schedule order;
// sql.ErrNoRows when it names none.
func LoadMatchState(ctx context.Context, q Queryer, sel MatchSelector) (DBMatchState, error) {
	matches, err := LoadMatchStates(ctx, q, sel)
	if err != nil {
		return DBMatchState{}, err
	}
	if len(matches) == 0 {
		return DBMatchState{}, sql.ErrNoRows
	}
	return matches[0], nil
}

// LoadMatchStates loads every match the selector names, in schedule order —
// stage, then match — with slots resolved into team states. Four statements
// however many matches: the headers, the slots, the rosters, the player names.
func LoadMatchStates(ctx context.Context, q Queryer, sel MatchSelector) ([]DBMatchState, error) {
	where, args := sel.where()
	rows, err := q.QueryContext(ctx, `
select m.id, m.game_id, g.game_type, m.code, m.title, m.status, m.revision, m.state_json,
       t.revision, t.updated_at, s.code, s.title, v.number, v.title, g.roster_source,
       coalesce(s.config_json, '')
from matches m
join fests t on t.id = m.fest_id
join games g on g.id = m.game_id
join stages s on s.id = m.stage_id
left join venues v on v.id = m.venue_id
where `+where+`
order by s.position, s.id, m.position, m.id`, args...)
	if err != nil {
		return nil, err
	}
	var matches []DBMatchState
	for rows.Next() {
		var match DBMatchState
		var updatedAt, stateJSON, stageConfig string
		var venueNumber sql.NullInt64
		var venueTitle sql.NullString
		if err := rows.Scan(&match.MatchID, &match.GameID, &match.GameType, &match.Code, &match.Title, &match.Status, &match.Revision, &stateJSON,
			&match.FestRevision, &updatedAt, &match.StageCode, &match.StageTitle, &venueNumber, &venueTitle, &match.RosterSource,
			&stageConfig); err != nil {
			rows.Close()
			return nil, err
		}
		match.Themes = stageThemeCount(stageConfig)
		match.RawState = stateJSON
		if TeamBlobShaped(match.GameType) {
			blob, err := ParseMatchBlob(stateJSON)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("match %d state: %w", match.MatchID, err)
			}
			match.Blob = blob
		}
		match.UpdatedAt = ParseDBTime(updatedAt)
		if venueNumber.Valid {
			match.Venue = &VenueView{Number: int(venueNumber.Int64), Title: venueTitle.String}
		}
		match.State = MatchState{
			Title:     match.Title,
			Finished:  match.Status == "finished",
			Revision:  match.Revision,
			UpdatedAt: match.UpdatedAt,
		}
		matches = append(matches, match)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(matches) == 0 {
		return matches, nil
	}

	slots, err := loadMatchSlots(ctx, q, matches)
	if err != nil {
		return nil, err
	}
	rosters, err := loadRosters(ctx, q, matches, slots)
	if err != nil {
		return nil, err
	}
	playerName, err := blobPlayerNames(ctx, q, matches)
	if err != nil {
		return nil, err
	}
	for i := range matches {
		match := &matches[i]
		for _, slot := range slots[match.MatchID] {
			for len(match.State.Participants) <= slot.Index {
				match.State.Participants = append(match.State.Participants, ParticipantState{})
				match.ParticipantIDs = append(match.ParticipantIDs, 0)
			}
			if !TeamBlobShaped(match.GameType) {
				name := slot.Name
				if !slot.ParticipantID.Valid {
					name = ParseSlotRef(slot.SourceType, slot.SourceRef).DisplayLabel()
				} else {
					match.ParticipantIDs[slot.Index] = slot.ParticipantID.Int64
				}
				match.State.Participants[slot.Index] = ParticipantState{
					ID:     match.ParticipantIDs[slot.Index],
					Name:   name,
					Roster: rosters[rosterKey{match.GameID, match.ParticipantIDs[slot.Index]}],
					Place:  slot.Place,
				}
				continue
			}
			if !slot.ParticipantID.Valid {
				match.State.Participants[slot.Index] = ParticipantState{
					Name:   ParseSlotRef(slot.SourceType, slot.SourceRef).DisplayLabel(),
					Themes: make([]ThemeEntry, ThemeCount),
				}
				continue
			}
			id := slot.ParticipantID.Int64
			match.State.Participants[slot.Index] = ParticipantStateFromBlob(
				match.Blob.Participants[strconv.FormatInt(id, 10)],
				id, slot.Name, rosters[rosterKey{match.GameID, id}], slot.Place, playerName)
			match.ParticipantIDs[slot.Index] = id
		}
		if TeamBlobShaped(match.GameType) {
			NormalizeStateTo(&match.State, match.Themes)
		}
	}
	return matches, nil
}

type slotRecord struct {
	Index         int
	ParticipantID sql.NullInt64
	Name          string
	Place         float64
	SourceType    string
	SourceRef     string
}

func matchIDs(matches []DBMatchState) []any {
	ids := make([]any, len(matches))
	for i, m := range matches {
		ids[i] = m.MatchID
	}
	return ids
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// loadMatchSlots reads every match's seats in one statement, by match id.
func loadMatchSlots(ctx context.Context, q Queryer, matches []DBMatchState) (map[int64][]slotRecord, error) {
	ids := matchIDs(matches)
	rows, err := q.QueryContext(ctx, `
select ms.match_id, ms.slot_index, ms.participant_id, coalesce(t.name, ''), coalesce(r.place, 0), ms.source_type, ms.source_ref_json
from match_slots ms
left join participants t on t.id = ms.participant_id
left join match_results r on r.match_id = ms.match_id and r.participant_id = ms.participant_id
where ms.match_id in (`+placeholders(len(ids))+`)
order by ms.match_id, ms.slot_index`, ids...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	slots := map[int64][]slotRecord{}
	for rows.Next() {
		var matchID int64
		var slot slotRecord
		if err := rows.Scan(&matchID, &slot.Index, &slot.ParticipantID, &slot.Name, &slot.Place, &slot.SourceType, &slot.SourceRef); err != nil {
			return nil, err
		}
		slots[matchID] = append(slots[matchID], slot)
	}
	return slots, rows.Err()
}

type rosterKey struct {
	gameID, participantID int64
}

// loadRosters reads the roster of every seated team of every team-blob match in
// one statement per roster source in play — a fest-wide roster or a game's own.
func loadRosters(ctx context.Context, q Queryer, matches []DBMatchState, slots map[int64][]slotRecord) (map[rosterKey][]RosterMember, error) {
	rosters := map[rosterKey][]RosterMember{}
	byGame := map[int64]*DBMatchState{}
	teams := map[int64]map[int64]bool{}
	for i := range matches {
		match := &matches[i]
		if !SeatsPlayers(match.GameType) {
			continue
		}
		byGame[match.GameID] = match
		for _, slot := range slots[match.MatchID] {
			if slot.ParticipantID.Valid {
				if teams[match.GameID] == nil {
					teams[match.GameID] = map[int64]bool{}
				}
				teams[match.GameID][slot.ParticipantID.Int64] = true
			}
		}
	}
	for gameID, ids := range teams {
		args := make([]any, 0, len(ids)+1)
		query := `
select tp.participant_id, p.id, p.first_name, p.last_name
from participant_players tp
join players p on p.id = tp.player_id
where tp.participant_id in (` + placeholders(len(ids)) + `)
order by tp.participant_id, tp.roster_order`
		if byGame[gameID].RosterSource == "game" {
			query = `
select gtp.participant_id, p.id, p.first_name, p.last_name
from game_team_players gtp
join players p on p.id = gtp.player_id
where gtp.game_id = ? and gtp.participant_id in (` + placeholders(len(ids)) + `)
order by gtp.participant_id, gtp.roster_order`
			args = append(args, gameID)
		}
		for id := range ids {
			args = append(args, id)
		}
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var participantID int64
			var member RosterMember
			var firstName, lastName string
			if err := rows.Scan(&participantID, &member.ID, &firstName, &lastName); err != nil {
				rows.Close()
				return nil, err
			}
			member.Name = JoinPlayerName(firstName, lastName)
			key := rosterKey{gameID, participantID}
			rosters[key] = append(rosters[key], member)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return rosters, nil
}

func stageThemeCount(configJSON string) int { return ParseStageConfig(configJSON).Themes() }

// blobPlayerNames resolves every player id the matches' blobs mention to a
// display name, in one statement.
func blobPlayerNames(ctx context.Context, q Queryer, matches []DBMatchState) (func(int64) string, error) {
	ids := map[int64]bool{}
	for _, match := range matches {
		for _, section := range match.Blob.Participants {
			for _, theme := range section.Themes {
				if theme.Player != 0 {
					ids[theme.Player] = true
				}
			}
			for _, theme := range section.ShootoutThemes {
				if theme.Player != 0 {
					ids[theme.Player] = true
				}
			}
		}
	}
	names := map[int64]string{}
	if len(ids) > 0 {
		args := make([]any, 0, len(ids))
		for id := range ids {
			args = append(args, id)
		}
		rows, err := q.QueryContext(ctx, `select id, first_name, last_name from players where id in (`+placeholders(len(ids))+`)`, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var first, last string
			if err := rows.Scan(&id, &first, &last); err != nil {
				return nil, err
			}
			names[id] = JoinPlayerName(first, last)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return func(id int64) string { return names[id] }, nil
}
