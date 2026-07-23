package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ThemeCount is the number of regular themes per team in a match.
const ThemeCount = 12

// QuestionValues is the EK/KSI per-answer point scale (lowest to highest).
var QuestionValues = [5]int{10, 20, 30, 40, 50}

// DBMatchState is a match's full state as loaded from the DB: the match header,
// its venue, the scored MatchState, and the per-slot team ids.
type DBMatchState struct {
	MatchID      int64
	GameID       int64
	Code         string
	Title        string
	Status       string
	Revision     int64
	FestRevision int64
	UpdatedAt    time.Time
	StageCode    string
	StageTitle   string
	Venue        *VenueView
	State        MatchState
	Blob         MatchBlob
	TeamIDs      []int64
	RosterSource string
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

// NormalizeState fills defaults and pads/clamps each team's themes to a uniform
// shape, normalising every answer mark, so a freshly-loaded or hand-edited
// state is well-formed before scoring/serving.
func NormalizeState(state *MatchState) {
	if state.Title == "" {
		state.Title = "Бой A"
	}
	if state.Revision == 0 {
		state.Revision = 1
	}
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now()
	}
	shootoutThemeCount := 0
	for i := range state.Teams {
		if len(state.Teams[i].ShootoutThemes) > shootoutThemeCount {
			shootoutThemeCount = len(state.Teams[i].ShootoutThemes)
		}
	}
	for i := range state.Teams {
		state.Teams[i].Tiebreak = 0
		if len(state.Teams[i].Themes) < ThemeCount {
			missing := ThemeCount - len(state.Teams[i].Themes)
			state.Teams[i].Themes = append(state.Teams[i].Themes, make([]ThemeEntry, missing)...)
		}
		if len(state.Teams[i].Themes) > ThemeCount {
			state.Teams[i].Themes = state.Teams[i].Themes[:ThemeCount]
		}
		for t := range state.Teams[i].Themes {
			for a := range state.Teams[i].Themes[t].Answers {
				state.Teams[i].Themes[t].Answers[a] = NormalizeMark(state.Teams[i].Themes[t].Answers[a])
			}
		}
		if len(state.Teams[i].ShootoutThemes) < shootoutThemeCount {
			missing := shootoutThemeCount - len(state.Teams[i].ShootoutThemes)
			state.Teams[i].ShootoutThemes = append(state.Teams[i].ShootoutThemes, make([]ThemeEntry, missing)...)
		}
		for t := range state.Teams[i].ShootoutThemes {
			for a := range state.Teams[i].ShootoutThemes[t].Answers {
				state.Teams[i].ShootoutThemes[t].Answers[a] = NormalizeMark(state.Teams[i].ShootoutThemes[t].Answers[a])
			}
		}
	}
}

// LoadDBMatchState loads a match by fest id and code.
func LoadDBMatchState(ctx context.Context, q Queryer, festID int64, code string) (DBMatchState, error) {
	return LoadDBMatchStateWhere(ctx, q, `m.fest_id = ? and m.code = ?`, festID, code)
}

// LoadDBMatchStateWhere loads the single match matching the where clause, with
// its slots resolved into team states.
func LoadDBMatchStateWhere(ctx context.Context, q Queryer, where string, args ...any) (DBMatchState, error) {
	var match DBMatchState
	var updatedAt string
	var venueNumber sql.NullInt64
	var venueTitle sql.NullString
	var stateJSON string
	if err := q.QueryRowContext(ctx, `
select m.id, m.game_id, m.code, m.title, m.status, m.revision, m.state_json,
       t.revision, t.updated_at, s.code, s.title, v.number, v.title, g.roster_source
from matches m
join fests t on t.id = m.fest_id
join games g on g.id = m.game_id
join stages s on s.id = m.stage_id
left join venues v on v.id = m.venue_id
where `+where, args...).
		Scan(&match.MatchID, &match.GameID, &match.Code, &match.Title, &match.Status, &match.Revision, &stateJSON,
			&match.FestRevision, &updatedAt, &match.StageCode, &match.StageTitle, &venueNumber, &venueTitle, &match.RosterSource); err != nil {
		return DBMatchState{}, err
	}
	blob, err := ParseMatchBlob(stateJSON)
	if err != nil {
		return DBMatchState{}, fmt.Errorf("match %d state: %w", match.MatchID, err)
	}
	match.Blob = blob
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

	slotRows, err := q.QueryContext(ctx, `
select ms.slot_index, ms.team_id, coalesce(t.name, ''), coalesce(r.place, 0), ms.source_type, ms.source_ref_json
from match_slots ms
left join teams t on t.id = ms.team_id
left join match_results r on r.match_id = ms.match_id and r.team_id = ms.team_id
where ms.match_id = ?
order by ms.slot_index`, match.MatchID)
	if err != nil {
		return DBMatchState{}, err
	}
	defer slotRows.Close()

	type slotRecord struct {
		Index      int
		TeamID     sql.NullInt64
		Name       string
		Place      float64
		SourceType string
		SourceRef  string
	}
	var slots []slotRecord
	for slotRows.Next() {
		var slotIndex int
		var teamID sql.NullInt64
		var name string
		var place float64
		var sourceType string
		var sourceRef string
		if err := slotRows.Scan(&slotIndex, &teamID, &name, &place, &sourceType, &sourceRef); err != nil {
			return DBMatchState{}, err
		}
		slots = append(slots, slotRecord{
			Index:      slotIndex,
			TeamID:     teamID,
			Name:       name,
			Place:      place,
			SourceType: sourceType,
			SourceRef:  sourceRef,
		})
	}
	if err := slotRows.Err(); err != nil {
		return DBMatchState{}, err
	}
	if err := slotRows.Close(); err != nil {
		return DBMatchState{}, err
	}
	playerName, err := blobPlayerNames(ctx, q, match.Blob)
	if err != nil {
		return DBMatchState{}, err
	}
	for _, slot := range slots {
		for len(match.State.Teams) <= slot.Index {
			match.State.Teams = append(match.State.Teams, TeamState{})
			match.TeamIDs = append(match.TeamIDs, 0)
		}
		if !slot.TeamID.Valid {
			match.State.Teams[slot.Index] = TeamState{
				Name:   SlotSourceLabel(slot.SourceType, slot.SourceRef),
				Themes: make([]ThemeEntry, ThemeCount),
			}
			continue
		}
		roster, err := LoadTeamRoster(ctx, q, match.GameID, match.RosterSource, slot.TeamID.Int64)
		if err != nil {
			return DBMatchState{}, err
		}
		match.State.Teams[slot.Index] = TeamStateFromBlob(
			match.Blob.Teams[strconv.FormatInt(slot.TeamID.Int64, 10)],
			slot.Name, roster, slot.Place, playerName)
		match.TeamIDs[slot.Index] = slot.TeamID.Int64
	}
	NormalizeState(&match.State)
	return match, nil
}

// blobPlayerNames batch-resolves every player id referenced by a match blob to
// its display name.
func blobPlayerNames(ctx context.Context, q Queryer, blob MatchBlob) (func(int64) string, error) {
	ids := map[int64]bool{}
	for _, section := range blob.Teams {
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
	names := map[int64]string{}
	if len(ids) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		args := make([]any, 0, len(ids))
		for id := range ids {
			args = append(args, id)
		}
		rows, err := q.QueryContext(ctx, `select id, first_name, last_name from players where id in (`+placeholders+`)`, args...)
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

// LoadTeamRoster loads one team's roster names, from the fest-wide roster or
// the game-scoped one per the game's roster_source.
func LoadTeamRoster(ctx context.Context, q Queryer, gameID int64, rosterSource string, teamID int64) ([]string, error) {
	rosterQuery := `
select p.first_name, p.last_name
from team_players tp
join players p on p.id = tp.player_id
where tp.team_id = ?
order by tp.roster_order`
	rosterArgs := []any{teamID}
	if rosterSource == "game" {
		rosterQuery = `
select p.first_name, p.last_name
from game_team_players gtp
join players p on p.id = gtp.player_id
where gtp.game_id = ? and gtp.team_id = ?
order by gtp.roster_order`
		rosterArgs = []any{gameID, teamID}
	}
	return CollectRows(ctx, q, rosterQuery, rosterArgs, func(rows *sql.Rows) (string, error) {
		var firstName, lastName string
		if err := rows.Scan(&firstName, &lastName); err != nil {
			return "", err
		}
		return JoinPlayerName(firstName, lastName), nil
	})
}
