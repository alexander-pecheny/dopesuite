package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

type seedImportView struct {
	Source       string              `json:"source,omitempty"`
	SourceGameID int64               `json:"sourceGameID,omitempty"`
	DrawSize     int                 `json:"drawSize"`
	ActiveCount  int                 `json:"activeCount"`
	Rows         []seedImportViewRow `json:"rows"`
}

type seedImportViewRow struct {
	SourceRank int    `json:"sourceRank"`
	SeedNumber int    `json:"seedNumber,omitempty"`
	TeamID     int64  `json:"teamID"`
	Name       string `json:"name"`
	City       string `json:"city,omitempty"`
	Declined   bool   `json:"declined"`
	Waitlist   bool   `json:"waitlist"`
}

type seedDeclineRequest struct {
	TeamID   int64 `json:"teamID"`
	Declined bool  `json:"declined"`
}

type ksiSeedCandidate struct {
	SourceIndex int
	SourceRank  int
	Name        string
	Metrics     ksiSeedMetrics
}

type ksiSeedMetrics struct {
	Total   int
	Plus    int
	Correct [5]int
}

type seedRosterTeam struct {
	Name    string
	City    string
	Players []seedRosterPlayer
}

type seedRosterPlayer struct {
	FirstName string
	LastName  string
}

func (s *server) handleScopedSeedImport(w http.ResponseWriter, r *http.Request, scope festScope, sub []string) {
	if _, ok := s.requireFestTableEditor(w, r, scope.FestID); !ok {
		return
	}
	if len(sub) == 0 {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		view, err := s.loadSeedImportView(r.Context(), scope)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSONValue(w, view)
		return
	}
	if len(sub) != 1 {
		http.NotFound(w, r)
		return
	}
	switch sub[0] {
	case "ksi":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		view, revision, stateJSON, err := s.importSeedsFromKSI(r.Context(), scope)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.broadcastState(scope.FestID, fmt.Sprintf("game-state:%d", scope.GameID), revision, stateJSON)
		writeJSONValue(w, view)
	case "decline":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()
		var req seedDeclineRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		view, revision, stateJSON, err := s.setSeedImportDeclined(r.Context(), scope, req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.broadcastState(scope.FestID, fmt.Sprintf("game-state:%d", scope.GameID), revision, stateJSON)
		writeJSONValue(w, view)
	default:
		http.NotFound(w, r)
	}
}

func (s *server) loadSeedImportView(ctx context.Context, scope festScope) (seedImportView, error) {
	var rawState string
	if err := s.db.QueryRowContext(ctx, `
select coalesce(state_json, '{}')
from games
where fest_id = ? and id = ? and game_type = 'ek'`, scope.FestID, scope.GameID).Scan(&rawState); err != nil {
		return seedImportView{}, err
	}
	state, err := seedImportStateFromRaw(rawState)
	if err != nil {
		return seedImportView{}, err
	}
	drawSize, err := maxSeedNumber(ctx, s.db, scope.GameID)
	if err != nil {
		return seedImportView{}, err
	}
	return buildSeedImportView(state, drawSize), nil
}

func (s *server) importSeedsFromKSI(ctx context.Context, scope festScope) (seedImportView, int64, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return seedImportView{}, 0, nil, err
	}
	defer tx.Rollback()

	rawState, err := loadEKGameStateForSeedImport(ctx, tx, scope)
	if err != nil {
		return seedImportView{}, 0, nil, err
	}
	previous, err := seedImportStateFromRaw(rawState)
	if err != nil {
		return seedImportView{}, 0, nil, err
	}
	previousDeclinesByTeam, previousDeclinesByName := previousSeedDeclines(previous)

	sourceGameID, candidates, err := loadKSISeedCandidates(ctx, tx, scope.FestID)
	if err != nil {
		return seedImportView{}, 0, nil, err
	}
	roster, err := loadSeedRosterTeams(ctx, tx, scope.FestID)
	if err != nil {
		return seedImportView{}, 0, nil, err
	}

	rows := make([]seedImportStateRow, 0, len(candidates))
	seenTeams := make(map[int64]string, len(candidates))
	for _, candidate := range candidates {
		teamRoster := roster[seedTeamNameKey(candidate.Name)]
		city := teamRoster.City
		teamID, city, err := ensureSeedTeam(ctx, tx, scope.FestID, candidate.Name, city, teamRoster.Players)
		if err != nil {
			return seedImportView{}, 0, nil, err
		}
		if previous, exists := seenTeams[teamID]; exists {
			return seedImportView{}, 0, nil, fmt.Errorf("КСИ содержит команду %q больше одного раза (первое имя: %q)", candidate.Name, previous)
		}
		seenTeams[teamID] = candidate.Name
		declined := previousDeclinesByTeam[teamID]
		if !declined {
			declined = previousDeclinesByName[seedTeamNameKey(candidate.Name)]
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
		return seedImportView{}, 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return seedImportView{}, 0, nil, err
	}
	return view, revision, stateJSON, nil
}

func (s *server) setSeedImportDeclined(ctx context.Context, scope festScope, req seedDeclineRequest) (seedImportView, int64, []byte, error) {
	if req.TeamID <= 0 {
		return seedImportView{}, 0, nil, errors.New("bad team id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return seedImportView{}, 0, nil, err
	}
	defer tx.Rollback()

	rawState, err := loadEKGameStateForSeedImport(ctx, tx, scope)
	if err != nil {
		return seedImportView{}, 0, nil, err
	}
	state, err := seedImportStateFromRaw(rawState)
	if err != nil {
		return seedImportView{}, 0, nil, err
	}
	if len(state.Rows) == 0 {
		return seedImportView{}, 0, nil, errors.New("сначала импортируйте команды из КСИ")
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
		return seedImportView{}, 0, nil, errors.New("команда не найдена в импорте посева")
	}

	view, revision, stateJSON, err := saveSeedImportState(ctx, tx, scope, rawState, state, "seed-import:decline")
	if err != nil {
		return seedImportView{}, 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return seedImportView{}, 0, nil, err
	}
	return view, revision, stateJSON, nil
}

func loadEKGameStateForSeedImport(ctx context.Context, q dbQueryer, scope festScope) (string, error) {
	var rawState string
	err := q.QueryRowContext(ctx, `
select coalesce(state_json, '{}')
from games
where fest_id = ? and id = ? and game_type = 'ek'`, scope.FestID, scope.GameID).Scan(&rawState)
	return rawState, err
}

func saveSeedImportState(ctx context.Context, tx *sql.Tx, scope festScope, previousRaw string, state seedImportState, eventType string) (seedImportView, int64, []byte, error) {
	stateJSON, err := putSeedImportState(previousRaw, state)
	if err != nil {
		return seedImportView{}, 0, nil, err
	}
	if _, err := tx.ExecContext(ctx, `
update games set state_json = ?, updated_at = ?
where fest_id = ? and id = ? and game_type = 'ek'`, string(stateJSON), utcNow(), scope.FestID, scope.GameID); err != nil {
		return seedImportView{}, 0, nil, err
	}
	assignments, err := replaceSeedAssignments(ctx, tx, scope.GameID, state.Rows)
	if err != nil {
		return seedImportView{}, 0, nil, err
	}
	if err := resolveSeedSlots(ctx, tx, scope.GameID, assignments); err != nil {
		return seedImportView{}, 0, nil, err
	}
	hasRosterOverrides, err := gameHasPlayerOverridesTx(ctx, tx, scope.FestID, scope.GameID)
	if err != nil {
		return seedImportView{}, 0, nil, err
	}
	if hasRosterOverrides {
		if err := materializeGameRosterOverridesTx(ctx, tx, scope.FestID, scope.GameID); err != nil {
			return seedImportView{}, 0, nil, err
		}
	}
	drawSize, err := maxSeedNumber(ctx, tx, scope.GameID)
	if err != nil {
		return seedImportView{}, 0, nil, err
	}
	revision, err := bumpFestRevisionTx(ctx, tx, scope.FestID, eventType, mustJSON(map[string]any{
		"gameID":       scope.GameID,
		"source":       state.Source,
		"sourceGameID": state.SourceGameID,
		"rows":         len(state.Rows),
	}))
	if err != nil {
		return seedImportView{}, 0, nil, err
	}
	return buildSeedImportView(state, drawSize), revision, stateJSON, nil
}

func seedImportStateFromRaw(raw string) (seedImportState, error) {
	obj, err := rawJSONObject(raw)
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
	obj, err := rawJSONObject(raw)
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
			byName[seedTeamNameKey(row.Name)] = true
		}
	}
	return byTeam, byName
}

func buildSeedImportView(state seedImportState, drawSize int) seedImportView {
	view := seedImportView{
		Source:       state.Source,
		SourceGameID: state.SourceGameID,
		DrawSize:     drawSize,
		Rows:         make([]seedImportViewRow, 0, len(state.Rows)),
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
		view.Rows = append(view.Rows, seedImportViewRow{
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
	rows, err := tx.QueryContext(ctx, `
select ms.id, ms.match_id, ms.source_ref_json
from match_slots ms
join matches m on m.id = ms.match_id
where m.game_id = ? and ms.source_type = 'seed' and ms.locked = 0
order by ms.id`, gameID)
	if err != nil {
		return err
	}
	defer rows.Close()

	type slotRecord struct {
		ID        int64
		MatchID   int64
		SourceRef string
	}
	var slots []slotRecord
	for rows.Next() {
		var slot slotRecord
		if err := rows.Scan(&slot.ID, &slot.MatchID, &slot.SourceRef); err != nil {
			return err
		}
		slots = append(slots, slot)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, slot := range slots {
		basket, number := seedRefKey(slot.SourceRef)
		teamID := assignments[[2]int{basket, number}]
		if _, err := tx.ExecContext(ctx, `update match_slots set team_id = ? where id = ?`, nullableInt64(teamID), slot.ID); err != nil {
			return err
		}
		if teamID > 0 {
			if err := ensureRegularThemes(ctx, tx, slot.MatchID, teamID); err != nil {
				return err
			}
		}
	}
	return nil
}

func ensureRegularThemes(ctx context.Context, tx *sql.Tx, matchID, teamID int64) error {
	for themeIndex := 0; themeIndex < themeCount; themeIndex++ {
		var exists int
		if err := tx.QueryRowContext(ctx, `
select count(*) from themes
where match_id = ? and team_id = ? and kind = 'regular' and theme_index = ?`,
			matchID, teamID, themeIndex).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		if err := insertTheme(ctx, tx, matchID, teamID, "regular", themeIndex, 0, [5]string{}); err != nil {
			return err
		}
	}
	return nil
}

func maxSeedNumber(ctx context.Context, q dbQueryer, gameID int64) (int, error) {
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
	basket := intFromMap(ref, "basket")
	if basket <= 0 {
		basket = 1
	}
	number := intFromMap(ref, "number")
	if number == 0 {
		number = intFromMap(ref, "position")
	}
	return basket, number
}

func loadKSISeedCandidates(ctx context.Context, q dbQueryer, festID int64) (int64, []ksiSeedCandidate, error) {
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

	participants, themes, err := decodeKSIStateForSeed(schemeJSON, stateJSON)
	if err != nil {
		return 0, nil, err
	}
	if len(participants) == 0 {
		return 0, nil, errors.New("в КСИ нет команд")
	}
	candidates := make([]ksiSeedCandidate, 0, len(participants))
	for index, name := range participants {
		name = strings.TrimSpace(name)
		if name == "" {
			name = fmt.Sprintf("Команда %d", index+1)
		}
		candidates = append(candidates, ksiSeedCandidate{
			SourceIndex: index,
			Name:        name,
			Metrics:     ksiMetricsForParticipant(themes, index),
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

func decodeKSIStateForSeed(schemeJSON, stateJSON string) ([]string, [][][]string, error) {
	var state struct {
		Participants []string `json:"participants"`
		Themes       []struct {
			Answers [][]string `json:"answers"`
		} `json:"themes"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return nil, nil, fmt.Errorf("не удалось разобрать состояние КСИ: %w", err)
	}
	participants := state.Participants
	if len(participants) == 0 {
		var scheme struct {
			Participants []string `json:"participants"`
		}
		if err := json.Unmarshal([]byte(schemeJSON), &scheme); err != nil {
			return nil, nil, fmt.Errorf("не удалось разобрать схему КСИ: %w", err)
		}
		participants = scheme.Participants
	}
	themes := make([][][]string, 0, len(state.Themes))
	for _, theme := range state.Themes {
		themes = append(themes, theme.Answers)
	}
	return participants, themes, nil
}

func ksiMetricsForParticipant(themes [][][]string, participantIndex int) ksiSeedMetrics {
	var metrics ksiSeedMetrics
	for _, answers := range themes {
		if participantIndex >= len(answers) {
			continue
		}
		row := answers[participantIndex]
		for answerIndex, mark := range row {
			if answerIndex >= len(questionValues) {
				break
			}
			value := questionValues[answerIndex]
			switch normalizeMark(mark) {
			case "right":
				metrics.Total += value
				metrics.Plus += value
				metrics.Correct[answerIndex]++
			case "wrong":
				metrics.Total -= value
			}
		}
	}
	return metrics
}

func compareKSISeedCandidates(a, b ksiSeedCandidate) int {
	if a.Metrics.Total != b.Metrics.Total {
		return b.Metrics.Total - a.Metrics.Total
	}
	if a.Metrics.Plus != b.Metrics.Plus {
		return b.Metrics.Plus - a.Metrics.Plus
	}
	for index := len(questionValues) - 1; index >= 0; index-- {
		if a.Metrics.Correct[index] != b.Metrics.Correct[index] {
			return b.Metrics.Correct[index] - a.Metrics.Correct[index]
		}
	}
	if cmp := compareAlpha(a.Name, b.Name); cmp != 0 {
		return cmp
	}
	return a.SourceIndex - b.SourceIndex
}

func loadSeedRosterTeams(ctx context.Context, q dbQueryer, festID int64) (map[string]seedRosterTeam, error) {
	rows, err := q.QueryContext(ctx, `
select id, name, city
from fest_teams
where fest_id = ? and deleted = 0
order by position, id`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type teamRow struct {
		ID   int64
		Name string
		City string
	}
	var teamRows []teamRow
	for rows.Next() {
		var row teamRow
		if err := rows.Scan(&row.ID, &row.Name, &row.City); err != nil {
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

	out := make(map[string]seedRosterTeam, len(teamRows))
	for _, row := range teamRows {
		players, err := loadSeedRosterPlayers(ctx, q, row.ID)
		if err != nil {
			return nil, err
		}
		key := seedTeamNameKey(row.Name)
		if _, exists := out[key]; exists {
			continue
		}
		out[key] = seedRosterTeam{Name: row.Name, City: row.City, Players: players}
	}
	return out, nil
}

func loadSeedRosterPlayers(ctx context.Context, q dbQueryer, festTeamID int64) ([]seedRosterPlayer, error) {
	rows, err := q.QueryContext(ctx, `
select p.first_name, p.last_name
from fest_team_players ftp
join fest_players p on p.id = ftp.player_id
where ftp.team_id = ?
order by ftp.roster_order, p.id`, festTeamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var players []seedRosterPlayer
	for rows.Next() {
		var player seedRosterPlayer
		if err := rows.Scan(&player.FirstName, &player.LastName); err != nil {
			return nil, err
		}
		players = append(players, player)
	}
	return players, rows.Err()
}

func ensureSeedTeam(ctx context.Context, tx *sql.Tx, festID int64, name, city string, players []seedRosterPlayer) (int64, string, error) {
	name = strings.TrimSpace(name)
	city = strings.TrimSpace(city)
	if name == "" {
		return 0, "", errors.New("empty team name")
	}

	var teamID int64
	var existingCity string
	err := tx.QueryRowContext(ctx, `
select id, city
from teams
where fest_id = ? and name = ?
order by case when city = ? then 0 when city = '' then 1 else 2 end, id
limit 1`, festID, name, city).Scan(&teamID, &existingCity)
	if errors.Is(err, sql.ErrNoRows) {
		teamID, err = insertReturningID(ctx, tx, `
insert into teams(fest_id, name, city)
values(?, ?, ?)`, festID, name, city)
		if err != nil {
			return 0, "", err
		}
		existingCity = city
	} else if err != nil {
		return 0, "", err
	}
	if city != "" && existingCity == "" {
		if _, err := tx.ExecContext(ctx, `update teams set city = ? where id = ?`, city, teamID); err != nil {
			return 0, "", err
		}
		existingCity = city
	}
	if len(players) > 0 {
		if err := replaceSeedTeamRoster(ctx, tx, festID, teamID, players); err != nil {
			return 0, "", err
		}
	}
	return teamID, existingCity, nil
}

func replaceSeedTeamRoster(ctx context.Context, tx *sql.Tx, festID, teamID int64, players []seedRosterPlayer) error {
	if _, err := tx.ExecContext(ctx, `delete from team_players where team_id = ?`, teamID); err != nil {
		return err
	}
	for rosterOrder, player := range players {
		firstName := strings.TrimSpace(player.FirstName)
		lastName := strings.TrimSpace(player.LastName)
		if firstName == "" && lastName == "" {
			continue
		}
		playerID, err := ensureSeedPlayer(ctx, tx, festID, firstName, lastName)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
insert or ignore into team_players(team_id, player_id, roster_order)
values(?, ?, ?)`, teamID, playerID, rosterOrder); err != nil {
			return err
		}
	}
	return nil
}

func ensureSeedPlayer(ctx context.Context, tx *sql.Tx, festID int64, firstName, lastName string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
select id from players
where fest_id = ? and first_name = ? and last_name = ?
order by id
limit 1`, festID, firstName, lastName).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return insertReturningID(ctx, tx, `
insert into players(fest_id, first_name, last_name)
values(?, ?, ?)`, festID, firstName, lastName)
	}
	return id, err
}

func seedTeamNameKey(name string) string {
	return alphaKey(name)
}
