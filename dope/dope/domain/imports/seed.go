package imports

import (
	"context"
	"database/sql"
	"dope/dope/domain/core"
	"dope/dope/domain/games"
	"dope/dope/domain/overrides"
	"dope/dope/domain/resolver"
	rosterpkg "dope/dope/domain/roster"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	seedImportStateKey = "seedImport"
	seedSourceKSI      = "ksi"
)

type seedImportState struct {
	Source       string               `json:"source,omitempty"`
	SourceGameID int64                `json:"sourceGameID,omitempty"`
	Rows         []seedImportStateRow `json:"rows,omitempty"`
}

type seedImportStateRow struct {
	SourceRank int    `json:"sourceRank"`
	TeamID     int64  `json:"teamID"`
	Name       string `json:"name"`
	City       string `json:"city,omitempty"`
	Declined   bool   `json:"declined,omitempty"`
}

type SeedImportView struct {
	Source       string              `json:"source,omitempty"`
	SourceGameID int64               `json:"sourceGameID,omitempty"`
	DrawSize     int                 `json:"drawSize"`
	ActiveCount  int                 `json:"activeCount"`
	Rows         []SeedImportViewRow `json:"rows"`
}

type SeedImportViewRow struct {
	SourceRank int    `json:"sourceRank"`
	SeedNumber int    `json:"seedNumber,omitempty"`
	TeamID     int64  `json:"teamID"`
	Name       string `json:"name"`
	City       string `json:"city,omitempty"`
	Declined   bool   `json:"declined"`
	Waitlist   bool   `json:"waitlist"`
}

type SeedDeclineRequest struct {
	TeamID   int64 `json:"teamID"`
	Declined bool  `json:"declined"`
}

type ksiSeedCandidate struct {
	SourceIndex int
	SourceRank  int
	Name        string
	Number      int  // team number (universal identity); 0 for legacy number-less KSI state
	Declined    bool // marked refused-to-play in the KSI «Отказы» tab
	Metrics     ksiSeedMetrics
}

type ksiSeedMetrics struct {
	Total   int
	Plus    int
	Correct [5]int
}

type seedRosterTeam struct {
	Number  int64
	Name    string
	City    string
	Players []rosterpkg.SeedRosterPlayer
}

func LoadSeedImportView(eng *core.Engine, ctx context.Context, scope core.FestScope) (SeedImportView, error) {
	var rawState string
	if err := eng.DB.QueryRowContext(ctx, `
select coalesce(state_json, '{}')
from games
where fest_id = ? and id = ? and game_type = 'ek'`, scope.FestID, scope.GameID).Scan(&rawState); err != nil {
		return SeedImportView{}, err
	}
	state, err := seedImportStateFromRaw(rawState)
	if err != nil {
		return SeedImportView{}, err
	}
	drawSize, err := maxSeedNumber(ctx, eng.DB, scope.GameID)
	if err != nil {
		return SeedImportView{}, err
	}
	return buildSeedImportView(state, drawSize), nil
}

func ImportSeedsFromKSI(eng *core.Engine, ctx context.Context, scope core.FestScope) (SeedImportView, int64, []byte, error) {
	eng.Mu.Lock()
	defer eng.Mu.Unlock()

	tx, err := eng.BeginWriteTx(ctx)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	defer tx.Rollback()

	rawState, err := loadEKGameStateForSeedImport(ctx, tx, scope)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	previous, err := seedImportStateFromRaw(rawState)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	previousDeclinesByTeam, previousDeclinesByName := previousSeedDeclines(previous)

	sourceGameID, candidates, err := loadKSISeedCandidates(ctx, tx, scope.FestID)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	roster, err := loadSeedRosterTeams(ctx, tx, scope.FestID)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}

	// Index the roster by number (the identity) and by name (to recover a number
	// for a legacy number-less KSI state). First entry wins on duplicates.
	rosterByNumber := make(map[int64]seedRosterTeam, len(roster))
	rosterByName := make(map[string]seedRosterTeam, len(roster))
	for _, rt := range roster {
		if rt.Number > 0 {
			if _, ok := rosterByNumber[rt.Number]; !ok {
				rosterByNumber[rt.Number] = rt
			}
		}
		if key := rosterpkg.SeedTeamNameKey(rt.Name); key != "" {
			if _, ok := rosterByName[key]; !ok {
				rosterByName[key] = rt
			}
		}
	}

	rows := make([]seedImportStateRow, 0, len(candidates))
	seenTeams := make(map[int64]string, len(candidates))
	for _, candidate := range candidates {
		// Prefer the KSI participant's own number; for a legacy number-less state
		// recover it from the numbered fest roster by name when unambiguous.
		number := int64(candidate.Number)
		rt := rosterByNumber[number]
		if number <= 0 {
			if m, ok := rosterByName[rosterpkg.SeedTeamNameKey(candidate.Name)]; ok {
				rt = m
				number = m.Number
			}
		}
		var teamID int64
		var city string
		if number > 0 {
			teamID, city, err = EnsureSeedTeamByNumber(ctx, tx, scope.FestID, number, candidate.Name, rt.City, rt.Players)
		} else {
			teamID, city, err = rosterpkg.EnsureSeedTeam(ctx, tx, scope.FestID, candidate.Name, rt.City, rt.Players)
		}
		if err != nil {
			return SeedImportView{}, 0, nil, err
		}
		if previous, exists := seenTeams[teamID]; exists {
			return SeedImportView{}, 0, nil, fmt.Errorf("КСИ содержит команду %q больше одного раза (первое имя: %q)", candidate.Name, previous)
		}
		seenTeams[teamID] = candidate.Name
		// A team that refused to play in KSI lands pre-declined on the EK import page
		// (visible but skipped in seeding). Union with any prior EK-side decline so a
		// re-import never silently un-declines a team.
		declined := candidate.Declined || previousDeclinesByTeam[teamID]
		if !declined {
			declined = previousDeclinesByName[rosterpkg.SeedTeamNameKey(candidate.Name)]
		}
		rows = append(rows, seedImportStateRow{
			SourceRank: candidate.SourceRank,
			TeamID:     teamID,
			Name:       candidate.Name,
			City:       city,
			Declined:   declined,
		})
	}

	nextState := seedImportState{
		Source:       seedSourceKSI,
		SourceGameID: sourceGameID,
		Rows:         rows,
	}
	view, revision, stateJSON, err := saveSeedImportState(ctx, tx, scope, rawState, nextState, "seed-import:ksi")
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return SeedImportView{}, 0, nil, err
	}
	return view, revision, stateJSON, nil
}

func SetSeedImportDeclined(eng *core.Engine, ctx context.Context, scope core.FestScope, req SeedDeclineRequest) (SeedImportView, int64, []byte, error) {
	if req.TeamID <= 0 {
		return SeedImportView{}, 0, nil, errors.New("bad team id")
	}

	eng.Mu.Lock()
	defer eng.Mu.Unlock()

	tx, err := eng.BeginWriteTx(ctx)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	defer tx.Rollback()

	rawState, err := loadEKGameStateForSeedImport(ctx, tx, scope)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	state, err := seedImportStateFromRaw(rawState)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	if len(state.Rows) == 0 {
		return SeedImportView{}, 0, nil, errors.New("сначала импортируйте команды из КСИ")
	}
	found := false
	for i := range state.Rows {
		if state.Rows[i].TeamID != req.TeamID {
			continue
		}
		state.Rows[i].Declined = req.Declined
		found = true
		break
	}
	if !found {
		return SeedImportView{}, 0, nil, errors.New("команда не найдена в импорте посева")
	}

	view, revision, stateJSON, err := saveSeedImportState(ctx, tx, scope, rawState, state, "seed-import:decline")
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return SeedImportView{}, 0, nil, err
	}
	return view, revision, stateJSON, nil
}

func loadEKGameStateForSeedImport(ctx context.Context, q store.Queryer, scope core.FestScope) (string, error) {
	var rawState string
	err := q.QueryRowContext(ctx, `
select coalesce(state_json, '{}')
from games
where fest_id = ? and id = ? and game_type = 'ek'`, scope.FestID, scope.GameID).Scan(&rawState)
	return rawState, err
}

func saveSeedImportState(ctx context.Context, tx *sql.Tx, scope core.FestScope, previousRaw string, state seedImportState, eventType string) (SeedImportView, int64, []byte, error) {
	stateJSON, err := putSeedImportState(previousRaw, state)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	if _, err := tx.ExecContext(ctx, `
update games set state_json = ?, updated_at = ?
where fest_id = ? and id = ? and game_type = 'ek'`, string(stateJSON), util.UtcNow(), scope.FestID, scope.GameID); err != nil {
		return SeedImportView{}, 0, nil, err
	}
	assignments, err := replaceSeedAssignments(ctx, tx, scope.GameID, state.Rows)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	if err := resolveSeedSlots(ctx, tx, scope.GameID, assignments); err != nil {
		return SeedImportView{}, 0, nil, err
	}
	hasRosterOverrides, err := overrides.GameHasPlayerOverridesTx(ctx, tx, scope.FestID, scope.GameID)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	if hasRosterOverrides {
		if err := overrides.MaterializeGameRosterOverridesTx(ctx, tx, scope.FestID, scope.GameID); err != nil {
			return SeedImportView{}, 0, nil, err
		}
	}
	drawSize, err := maxSeedNumber(ctx, tx, scope.GameID)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	revision, err := festwrite.BumpFestRevisionTx(ctx, tx, scope.FestID, eventType, util.MustJSON(map[string]any{
		"gameID":       scope.GameID,
		"source":       state.Source,
		"sourceGameID": state.SourceGameID,
		"rows":         len(state.Rows),
	}))
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	return buildSeedImportView(state, drawSize), revision, stateJSON, nil
}

func seedImportStateFromRaw(raw string) (seedImportState, error) {
	obj, err := rosterpkg.RawJSONObject(raw)
	if err != nil {
		return seedImportState{}, err
	}
	rawState, ok := obj[seedImportStateKey]
	if !ok || len(rawState) == 0 {
		return seedImportState{}, nil
	}
	var state seedImportState
	if err := json.Unmarshal(rawState, &state); err != nil {
		return seedImportState{}, err
	}
	return state, nil
}

func putSeedImportState(raw string, state seedImportState) ([]byte, error) {
	obj, err := rosterpkg.RawJSONObject(raw)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	obj[seedImportStateKey] = data
	return json.Marshal(obj)
}

func previousSeedDeclines(state seedImportState) (map[int64]bool, map[string]bool) {
	byTeam := make(map[int64]bool, len(state.Rows))
	byName := make(map[string]bool, len(state.Rows))
	for _, row := range state.Rows {
		if !row.Declined {
			continue
		}
		if row.TeamID > 0 {
			byTeam[row.TeamID] = true
		}
		if row.Name != "" {
			byName[rosterpkg.SeedTeamNameKey(row.Name)] = true
		}
	}
	return byTeam, byName
}

func buildSeedImportView(state seedImportState, drawSize int) SeedImportView {
	view := SeedImportView{
		Source:       state.Source,
		SourceGameID: state.SourceGameID,
		DrawSize:     drawSize,
		Rows:         make([]SeedImportViewRow, 0, len(state.Rows)),
	}
	active := 0
	for _, row := range state.Rows {
		waitlist := drawSize > 0 && active >= drawSize
		seedNumber := 0
		if !row.Declined {
			active++
			seedNumber = active
			waitlist = drawSize > 0 && seedNumber > drawSize
		}
		view.Rows = append(view.Rows, SeedImportViewRow{
			SourceRank: row.SourceRank,
			SeedNumber: seedNumber,
			TeamID:     row.TeamID,
			Name:       row.Name,
			City:       row.City,
			Declined:   row.Declined,
			Waitlist:   waitlist,
		})
	}
	view.ActiveCount = active
	return view
}

func replaceSeedAssignments(ctx context.Context, tx *sql.Tx, gameID int64, rows []seedImportStateRow) (map[[2]int]int64, error) {
	if _, err := tx.ExecContext(ctx, `delete from game_assignments where game_id = ?`, gameID); err != nil {
		return nil, err
	}
	assignments := make(map[[2]int]int64, len(rows))
	seedNumber := 0
	for _, row := range rows {
		if row.Declined || row.TeamID <= 0 {
			continue
		}
		seedNumber++
		if _, err := tx.ExecContext(ctx, `
insert into game_assignments(game_id, basket, number, team_id, player_id)
values(?, 1, ?, ?, null)`, gameID, seedNumber, row.TeamID); err != nil {
			return nil, err
		}
		assignments[[2]int{1, seedNumber}] = row.TeamID
	}
	return assignments, nil
}

func resolveSeedSlots(ctx context.Context, tx *sql.Tx, gameID int64, assignments map[[2]int]int64) error {
	type slotRecord struct {
		ID        int64
		MatchID   int64
		SourceRef string
	}
	slots, err := store.CollectRows(ctx, tx, `
select ms.id, ms.match_id, ms.source_ref_json
from match_slots ms
join matches m on m.id = ms.match_id
where m.game_id = ? and ms.source_type = 'seed' and ms.locked = 0
order by ms.id`, []any{gameID}, func(rows *sql.Rows) (slotRecord, error) {
		var slot slotRecord
		if err := rows.Scan(&slot.ID, &slot.MatchID, &slot.SourceRef); err != nil {
			return slot, err
		}
		return slot, nil
	})
	if err != nil {
		return err
	}
	touchedMatches := make(map[int64]struct{})

	for _, slot := range slots {
		basket, number := seedRefKey(slot.SourceRef)
		teamID := assignments[[2]int{basket, number}]
		touchedMatches[slot.MatchID] = struct{}{}
		if _, err := tx.ExecContext(ctx, `update match_slots set team_id = ? where id = ?`, util.NullableInt64(teamID), slot.ID); err != nil {
			return err
		}
		if teamID > 0 {
			if err := resolver.EnsureRegularThemes(ctx, tx, slot.MatchID, teamID); err != nil {
				return err
			}
		}
	}
	for matchID := range touchedMatches {
		if err := pruneMatchStateToSlots(ctx, tx, matchID); err != nil {
			return err
		}
	}
	return nil
}

func pruneMatchStateToSlots(ctx context.Context, tx *sql.Tx, matchID int64) error {
	if _, err := tx.ExecContext(ctx, `
delete from match_results
where match_id = ?
  and not exists (
    select 1
    from match_slots ms
    where ms.match_id = match_results.match_id
      and ms.team_id = match_results.team_id
  )`, matchID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
delete from themes
where match_id = ?
  and not exists (
    select 1
    from match_slots ms
    where ms.match_id = themes.match_id
      and ms.team_id = themes.team_id
  )`, matchID)
	return err
}

func maxSeedNumber(ctx context.Context, q store.Queryer, gameID int64) (int, error) {
	rows, err := q.QueryContext(ctx, `
select ms.source_ref_json
from match_slots ms
join matches m on m.id = ms.match_id
where m.game_id = ? and ms.source_type = 'seed'`, gameID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	maxNumber := 0
	for rows.Next() {
		var sourceRef string
		if err := rows.Scan(&sourceRef); err != nil {
			return 0, err
		}
		_, number := seedRefKey(sourceRef)
		if number > maxNumber {
			maxNumber = number
		}
	}
	return maxNumber, rows.Err()
}

func seedRefKey(sourceRef string) (int, int) {
	var ref map[string]any
	_ = json.Unmarshal([]byte(sourceRef), &ref)
	basket := store.IntFromMap(ref, "basket")
	if basket <= 0 {
		basket = 1
	}
	number := store.IntFromMap(ref, "number")
	if number == 0 {
		number = store.IntFromMap(ref, "position")
	}
	return basket, number
}

func loadKSISeedCandidates(ctx context.Context, q store.Queryer, festID int64) (int64, []ksiSeedCandidate, error) {
	var sourceGameID int64
	var schemeJSON, stateJSON string
	if err := q.QueryRowContext(ctx, `
select id, coalesce(scheme_json, '{}'), coalesce(state_json, '{}')
from games
where fest_id = ? and game_type = 'ksi'
order by position, id
limit 1`, festID).Scan(&sourceGameID, &schemeJSON, &stateJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil, errors.New("в фесте нет игры КСИ")
		}
		return 0, nil, err
	}

	participants, ksiState, declined, err := decodeKSIStateForSeed(schemeJSON, stateJSON)
	if err != nil {
		return 0, nil, err
	}
	if len(participants) == 0 {
		return 0, nil, errors.New("в КСИ нет команд")
	}
	candidates := make([]ksiSeedCandidate, 0, len(participants))
	for index, p := range participants {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			name = fmt.Sprintf("Команда %d", index+1)
		}
		candidates = append(candidates, ksiSeedCandidate{
			SourceIndex: index,
			Name:        name,
			Number:      p.Number,
			Declined:    games.KSIParticipantDeclined(declined, p),
			Metrics:     ksiMetricsForParticipant(ksiState, index),
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return compareKSISeedCandidates(candidates[i], candidates[j]) < 0
	})
	for i := range candidates {
		candidates[i].SourceRank = i + 1
	}
	return sourceGameID, candidates, nil
}

// ksiSeedState bundles the per-theme answer grid with the optional sticker grid
// (and a flag for the stickers variant) so seed scoring can apply the sticker
// rules consistently with the UI and the xlsx export.
type ksiSeedState struct {
	themes       [][][]string
	stickers     [][]string
	stickersMode bool
}

func decodeKSIStateForSeed(schemeJSON, stateJSON string) ([]games.KSIParticipant, ksiSeedState, map[string]bool, error) {
	var state struct {
		Participants json.RawMessage `json:"participants"`
		Declined     map[string]bool `json:"declined"`
		Stickers     [][]string      `json:"stickers"`
		Themes       []struct {
			Answers [][]string `json:"answers"`
		} `json:"themes"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return nil, ksiSeedState{}, nil, fmt.Errorf("не удалось разобрать состояние КСИ: %w", err)
	}
	// Participants may be the current [{number,name}] objects or legacy names.
	participants := rosterpkg.ParseKSIParticipants(state.Participants)
	var scheme struct {
		Participants json.RawMessage        `json:"participants"`
		Stickers     games.KSIStickerConfig `json:"stickers"`
	}
	schemeErr := json.Unmarshal([]byte(schemeJSON), &scheme)
	if len(participants) == 0 {
		if schemeErr != nil {
			return nil, ksiSeedState{}, nil, fmt.Errorf("не удалось разобрать схему КСИ: %w", schemeErr)
		}
		participants = rosterpkg.ParseKSIParticipants(scheme.Participants)
	}
	themes := make([][][]string, 0, len(state.Themes))
	for _, theme := range state.Themes {
		themes = append(themes, theme.Answers)
	}
	seedState := ksiSeedState{
		themes:       themes,
		stickers:     state.Stickers,
		stickersMode: schemeErr == nil && len(scheme.Stickers.Types) > 0,
	}
	return participants, seedState, state.Declined, nil
}

func ksiMetricsForParticipant(state ksiSeedState, participantIndex int) ksiSeedMetrics {
	var metrics ksiSeedMetrics
	for themeIndex, answers := range state.themes {
		if participantIndex >= len(answers) {
			continue
		}
		sticker, scored := ksiSeedThemeSticker(state, themeIndex, participantIndex)
		if !scored {
			continue
		}
		row := answers[participantIndex]
		for answerIndex, mark := range row {
			if answerIndex >= len(store.QuestionValues) {
				break
			}
			value := store.QuestionValues[answerIndex]
			mark = store.NormalizeMark(mark)
			cv := games.KSIStickerMarkValue(sticker, mark, value)
			metrics.Total += cv
			if cv > 0 {
				metrics.Plus += cv
			}
			if mark == "right" {
				metrics.Correct[answerIndex]++
			}
		}
	}
	return metrics
}

// ksiSeedThemeSticker mirrors the export's ksiThemeSticker: plain KSI games score
// every theme under neutral rules; stickers games leave a theme unscored until
// its (team, theme) sticker is set.
func ksiSeedThemeSticker(state ksiSeedState, theme, player int) (string, bool) {
	if !state.stickersMode {
		return games.KSIStickerNeutral, true
	}
	if theme < len(state.stickers) && player < len(state.stickers[theme]) {
		if id := state.stickers[theme][player]; id != "" {
			return id, true
		}
	}
	return "", false
}

func compareKSISeedCandidates(a, b ksiSeedCandidate) int {
	if a.Metrics.Total != b.Metrics.Total {
		return b.Metrics.Total - a.Metrics.Total
	}
	if a.Metrics.Plus != b.Metrics.Plus {
		return b.Metrics.Plus - a.Metrics.Plus
	}
	for index := len(store.QuestionValues) - 1; index >= 0; index-- {
		if a.Metrics.Correct[index] != b.Metrics.Correct[index] {
			return b.Metrics.Correct[index] - a.Metrics.Correct[index]
		}
	}
	if cmp := util.CompareAlpha(a.Name, b.Name); cmp != 0 {
		return cmp
	}
	return a.SourceIndex - b.SourceIndex
}

func loadSeedRosterTeams(ctx context.Context, q store.Queryer, festID int64) ([]seedRosterTeam, error) {
	rows, err := q.QueryContext(ctx, `
select id, coalesce(number, 0), name, city
from fest_teams
where fest_id = ? and deleted = 0
order by position, id`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type teamRow struct {
		ID     int64
		Number int64
		Name   string
		City   string
	}
	var teamRows []teamRow
	for rows.Next() {
		var row teamRow
		if err := rows.Scan(&row.ID, &row.Number, &row.Name, &row.City); err != nil {
			return nil, err
		}
		teamRows = append(teamRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	out := make([]seedRosterTeam, 0, len(teamRows))
	for _, row := range teamRows {
		players, err := loadSeedRosterPlayers(ctx, q, row.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, seedRosterTeam{Number: row.Number, Name: row.Name, City: row.City, Players: players})
	}
	return out, nil
}

func loadSeedRosterPlayers(ctx context.Context, q store.Queryer, festTeamID int64) ([]rosterpkg.SeedRosterPlayer, error) {
	return store.CollectRows(ctx, q, `
select p.first_name, p.last_name
from fest_team_players ftp
join fest_players p on p.id = ftp.player_id
where ftp.team_id = ?
order by ftp.roster_order, p.id`, []any{festTeamID}, func(rows *sql.Rows) (rosterpkg.SeedRosterPlayer, error) {
		var player rosterpkg.SeedRosterPlayer
		if err := rows.Scan(&player.FirstName, &player.LastName); err != nil {
			return player, err
		}
		return player, nil
	})
}

// EnsureSeedTeamByNumber finds or creates the game-scoped team identified by its
// universal NUMBER (not name), updating name/city to the current rosterpkg. Because
// number is the identity, two same-named teams stay distinct and re-seeding
// follows a team across name changes — the EK side of the team-number
// unification. Falls back to name-keyed rosterpkg.EnsureSeedTeam when no number is known.
func EnsureSeedTeamByNumber(ctx context.Context, tx *sql.Tx, festID, number int64, name, city string, players []rosterpkg.SeedRosterPlayer) (int64, string, error) {
	name = strings.TrimSpace(name)
	city = strings.TrimSpace(city)
	if number <= 0 {
		return rosterpkg.EnsureSeedTeam(ctx, tx, festID, name, city, players)
	}
	if name == "" {
		return 0, "", errors.New("empty team name")
	}
	var teamID int64
	var existingName, existingCity string
	err := tx.QueryRowContext(ctx, `
select id, name, city from teams where fest_id = ? and number = ? limit 1`, festID, number).Scan(&teamID, &existingName, &existingCity)
	if errors.Is(err, sql.ErrNoRows) {
		teamID, err = store.InsertReturningID(ctx, tx, `
insert into teams(fest_id, name, city, number) values(?, ?, ?, ?)`, festID, name, city, number)
		if err != nil {
			return 0, "", err
		}
		existingCity = city
	} else if err != nil {
		return 0, "", err
	} else {
		// Keep the display name/city in step with the current rosterpkg.
		newName, newCity := existingName, existingCity
		if name != "" {
			newName = name
		}
		if city != "" {
			newCity = city
		}
		if newName != existingName || newCity != existingCity {
			if _, err := tx.ExecContext(ctx, `update teams set name = ?, city = ? where id = ?`, newName, newCity, teamID); err != nil {
				return 0, "", err
			}
			existingCity = newCity
		}
	}
	if len(players) > 0 {
		if err := rosterpkg.ReplaceSeedTeamRoster(ctx, tx, festID, teamID, players); err != nil {
			return 0, "", err
		}
	}
	return teamID, existingCity, nil
}
