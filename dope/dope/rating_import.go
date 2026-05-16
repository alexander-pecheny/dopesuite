package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const ratingResultsURL = "https://api.rating.chgk.net/tournaments/%d/results.json?includeTeamMembers=1"

type ratingRosterImportResult struct {
	TeamCount    int
	PlayerCount  int
	ODGameCount  int
	KSIGameCount int
}

type festRosterImportTeam struct {
	RatingID int64
	Name     string
	City     string
	Players  []festRosterImportPlayer
}

type festRosterImportPlayer struct {
	RatingID  int64
	FirstName string
	LastName  string
}

type ratingFestResult struct {
	Team        ratingTeam         `json:"team"`
	Current     ratingTeam         `json:"current"`
	TeamMembers []ratingTeamMember `json:"teamMembers"`
}

type ratingTeam struct {
	ID   int64       `json:"id"`
	Name string      `json:"name"`
	Town *ratingTown `json:"town"`
}

type ratingTown struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type ratingTeamMember struct {
	Player ratingPlayer `json:"player"`
}

type ratingPlayer struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Surname string `json:"surname"`
}

type chgkTeamJSON struct {
	Name string `json:"name"`
	City string `json:"city,omitempty"`
}

type gameStateBroadcast struct {
	GameID    int64
	StateJSON []byte
}

func (s *server) fetchAndImportRatingRoster(ctx context.Context, festID, ratingID int64) (ratingRosterImportResult, error) {
	teams, err := fetchRatingFestRoster(ctx, ratingID)
	if err != nil {
		return ratingRosterImportResult{}, err
	}
	return s.importFestRoster(ctx, festID, ratingID, teams)
}

func fetchRatingFestRoster(ctx context.Context, ratingID int64) ([]festRosterImportTeam, error) {
	if ratingID <= 0 {
		return nil, errors.New("rating fest id must be positive")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(ratingResultsURL, ratingID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("не удалось запросить рейтинг: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		detail := strings.TrimSpace(string(body))
		if detail == "" {
			detail = resp.Status
		}
		return nil, fmt.Errorf("рейтинг вернул ошибку: %s", detail)
	}

	var results []ratingFestResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("не удалось разобрать ответ рейтинга: %w", err)
	}
	return ratingResultsToFestRoster(results)
}

func ratingResultsToFestRoster(results []ratingFestResult) ([]festRosterImportTeam, error) {
	teams := make([]festRosterImportTeam, 0, len(results))
	for index, result := range results {
		name := strings.TrimSpace(result.Current.Name)
		if name == "" {
			name = strings.TrimSpace(result.Team.Name)
		}
		if name == "" {
			return nil, fmt.Errorf("team %d has no name", index+1)
		}
		city := ratingTownName(result.Current.Town)
		if city == "" {
			city = ratingTownName(result.Team.Town)
		}
		team := festRosterImportTeam{
			RatingID: result.Team.ID,
			Name:     name,
			City:     city,
			Players:  make([]festRosterImportPlayer, 0, len(result.TeamMembers)),
		}
		for memberIndex, member := range result.TeamMembers {
			firstName := strings.TrimSpace(member.Player.Name)
			lastName := strings.TrimSpace(member.Player.Surname)
			if firstName == "" && lastName == "" {
				return nil, fmt.Errorf("team %q player %d has no name", name, memberIndex+1)
			}
			team.Players = append(team.Players, festRosterImportPlayer{
				RatingID:  member.Player.ID,
				FirstName: firstName,
				LastName:  lastName,
			})
		}
		if len(team.Players) > 9 {
			return nil, fmt.Errorf("состав %q больше 9 игроков", name)
		}
		teams = append(teams, team)
	}
	return teams, nil
}

func ratingTownName(town *ratingTown) string {
	if town == nil {
		return ""
	}
	return strings.TrimSpace(town.Name)
}

func (s *server) importFestRoster(ctx context.Context, festID, ratingID int64, teams []festRosterImportTeam) (ratingRosterImportResult, error) {
	if s.db == nil {
		return ratingRosterImportResult{}, errors.New("sqlite is not enabled")
	}
	if festID <= 0 {
		return ratingRosterImportResult{}, errors.New("bad fest id")
	}
	if len(teams) == 0 {
		return ratingRosterImportResult{}, errors.New("рейтинг не вернул команды")
	}

	var updates []gameStateBroadcast
	var revision int64
	result, err := func() (ratingRosterImportResult, error) {
		s.mu.Lock()
		defer s.mu.Unlock()

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return ratingRosterImportResult{}, err
		}
		defer tx.Rollback()

		var exists int
		if err := tx.QueryRowContext(ctx, `select count(*) from fests where id = ?`, festID).Scan(&exists); err != nil {
			return ratingRosterImportResult{}, err
		}
		if exists == 0 {
			return ratingRosterImportResult{}, sql.ErrNoRows
		}

		if _, err := tx.ExecContext(ctx, `delete from fest_teams where fest_id = ?`, festID); err != nil {
			return ratingRosterImportResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `delete from fest_players where fest_id = ?`, festID); err != nil {
			return ratingRosterImportResult{}, err
		}

		playerIDs := make(map[string]int64)
		playerCount := 0
		for fallbackPosition, team := range teams {
			importOrder := fallbackPosition + 1
			teamID, err := insertReturningID(ctx, tx, `
insert into fest_teams(fest_id, rating_id, name, city, position)
values(?, ?, ?, ?, ?)`, festID, nullableInt64(team.RatingID), team.Name, team.City, importOrder)
			if err != nil {
				return ratingRosterImportResult{}, err
			}
			for rosterOrder, player := range team.Players {
				key := rosterPlayerKey(player)
				playerID := playerIDs[key]
				if playerID == 0 {
					playerID, err = insertReturningID(ctx, tx, `
insert into fest_players(fest_id, rating_id, first_name, last_name)
values(?, ?, ?, ?)`, festID, nullableInt64(player.RatingID), player.FirstName, player.LastName)
					if err != nil {
						return ratingRosterImportResult{}, err
					}
					playerIDs[key] = playerID
					playerCount++
				}
				if _, err := tx.ExecContext(ctx, `
insert into fest_team_players(team_id, player_id, roster_order)
values(?, ?, ?)`, teamID, playerID, rosterOrder); err != nil {
					return ratingRosterImportResult{}, err
				}
			}
		}

		chgkUpdates, err := propagateRosterToChGKTx(ctx, tx, festID, teams)
		if err != nil {
			return ratingRosterImportResult{}, err
		}
		ksiUpdates, err := propagateRosterToKSITx(ctx, tx, festID, teams)
		if err != nil {
			return ratingRosterImportResult{}, err
		}
		updates = append(chgkUpdates, ksiUpdates...)
		if _, err := tx.ExecContext(ctx, `update fests set rating_id = ?, updated_at = ? where id = ?`, ratingID, utcNow(), festID); err != nil {
			return ratingRosterImportResult{}, err
		}
		revision, err = bumpFestRevisionTx(ctx, tx, festID, "rating:roster-import", mustJSON(map[string]any{
			"ratingID": ratingID,
			"teams":    len(teams),
			"players":  playerCount,
			"odGames":  len(chgkUpdates),
			"ksiGames": len(ksiUpdates),
		}))
		if err != nil {
			return ratingRosterImportResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return ratingRosterImportResult{}, err
		}

		return ratingRosterImportResult{
			TeamCount:    len(teams),
			PlayerCount:  playerCount,
			ODGameCount:  len(chgkUpdates),
			KSIGameCount: len(ksiUpdates),
		}, nil
	}()
	if err != nil {
		return ratingRosterImportResult{}, err
	}

	for _, update := range updates {
		s.broadcastState(festID, fmt.Sprintf("game-state:%d", update.GameID), revision, update.StateJSON)
	}
	return result, nil
}

func rosterPlayerKey(player festRosterImportPlayer) string {
	if player.RatingID > 0 {
		return "rating:" + strconv.FormatInt(player.RatingID, 10)
	}
	return "name:" + strings.ToLower(joinPlayerName(player.FirstName, player.LastName))
}

func propagateRosterToChGKTx(ctx context.Context, tx *sql.Tx, festID int64, teams []festRosterImportTeam) ([]gameStateBroadcast, error) {
	rows, err := tx.QueryContext(ctx, `
select id, coalesce(scheme_json, '{}'), coalesce(state_json, '{}')
from games
where fest_id = ? and game_type = 'od'
order by position, id`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type odGameRecord struct {
		ID     int64
		Scheme string
		State  string
	}
	var games []odGameRecord
	for rows.Next() {
		var game odGameRecord
		if err := rows.Scan(&game.ID, &game.Scheme, &game.State); err != nil {
			return nil, err
		}
		games = append(games, game)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	updates := make([]gameStateBroadcast, 0, len(games))
	for _, game := range games {
		schemeJSON, err := applyRosterToChGKScheme(game.Scheme, teams)
		if err != nil {
			return nil, fmt.Errorf("game %d scheme: %w", game.ID, err)
		}
		stateJSON, err := applyRosterToChGKState(game.State, teams)
		if err != nil {
			return nil, fmt.Errorf("game %d state: %w", game.ID, err)
		}
		if _, err := tx.ExecContext(ctx, `
update games set scheme_json = ?, state_json = ?, updated_at = ?
where id = ? and fest_id = ?`, string(schemeJSON), string(stateJSON), utcNow(), game.ID, festID); err != nil {
			return nil, err
		}
		updates = append(updates, gameStateBroadcast{GameID: game.ID, StateJSON: stateJSON})
	}
	return updates, nil
}

func propagateRosterToKSITx(ctx context.Context, tx *sql.Tx, festID int64, teams []festRosterImportTeam) ([]gameStateBroadcast, error) {
	rows, err := tx.QueryContext(ctx, `
select id, coalesce(scheme_json, '{}'), coalesce(state_json, '{}')
from games
where fest_id = ? and game_type = 'ksi'
order by position, id`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type ksiGameRecord struct {
		ID     int64
		Scheme string
		State  string
	}
	var games []ksiGameRecord
	for rows.Next() {
		var game ksiGameRecord
		if err := rows.Scan(&game.ID, &game.Scheme, &game.State); err != nil {
			return nil, err
		}
		games = append(games, game)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	updates := make([]gameStateBroadcast, 0, len(games))
	for _, game := range games {
		schemeJSON, err := applyRosterToKSIScheme(game.Scheme, teams)
		if err != nil {
			return nil, fmt.Errorf("game %d scheme: %w", game.ID, err)
		}
		stateJSON, err := applyRosterToKSIState(game.State, teams)
		if err != nil {
			return nil, fmt.Errorf("game %d state: %w", game.ID, err)
		}
		if _, err := tx.ExecContext(ctx, `
update games set scheme_json = ?, state_json = ?, updated_at = ?
where id = ? and fest_id = ?`, string(schemeJSON), string(stateJSON), utcNow(), game.ID, festID); err != nil {
			return nil, err
		}
		updates = append(updates, gameStateBroadcast{GameID: game.ID, StateJSON: stateJSON})
	}
	return updates, nil
}

func applyRosterToChGKScheme(raw string, teams []festRosterImportTeam) ([]byte, error) {
	obj, err := rawJSONObject(raw)
	if err != nil {
		return nil, err
	}
	teamJSON, err := json.Marshal(chgkTeamsFromRoster(teams))
	if err != nil {
		return nil, err
	}
	nTeamsJSON, err := json.Marshal(len(teams))
	if err != nil {
		return nil, err
	}
	obj["teams"] = teamJSON
	obj["nTeams"] = nTeamsJSON
	return json.Marshal(obj)
}

func applyRosterToChGKState(raw string, teams []festRosterImportTeam) ([]byte, error) {
	obj, err := rawJSONObject(raw)
	if err != nil {
		return nil, err
	}
	teamJSON, err := json.Marshal(chgkTeamsFromRoster(teams))
	if err != nil {
		return nil, err
	}
	obj["teams"] = teamJSON

	if rawEntries, ok := obj["entries"]; ok && len(rawEntries) > 0 {
		var entries [][]int
		if err := json.Unmarshal(rawEntries, &entries); err == nil {
			for i := range entries {
				entries[i] = resizeIntSlice(entries[i], len(teams))
			}
			entriesJSON, err := json.Marshal(entries)
			if err != nil {
				return nil, err
			}
			obj["entries"] = entriesJSON
		}
	}
	delete(obj, "answers")
	delete(obj, "finished")
	return json.Marshal(obj)
}

func applyRosterToKSIScheme(raw string, teams []festRosterImportTeam) ([]byte, error) {
	obj, err := rawJSONObject(raw)
	if err != nil {
		return nil, err
	}
	participantsJSON, err := json.Marshal(teamNamesFromRoster(teams))
	if err != nil {
		return nil, err
	}
	gameTypeJSON, err := json.Marshal("ksi")
	if err != nil {
		return nil, err
	}
	themesJSON, err := json.Marshal(ksiThemeCount)
	if err != nil {
		return nil, err
	}
	obj["gameType"] = gameTypeJSON
	obj["participants"] = participantsJSON
	obj["themes"] = themesJSON
	return json.Marshal(obj)
}

func applyRosterToKSIState(raw string, teams []festRosterImportTeam) ([]byte, error) {
	obj, err := rawJSONObject(raw)
	if err != nil {
		return nil, err
	}
	participants := teamNamesFromRoster(teams)
	participantsJSON, err := json.Marshal(participants)
	if err != nil {
		return nil, err
	}
	obj["participants"] = participantsJSON

	var themes []map[string]json.RawMessage
	if rawThemes, ok := obj["themes"]; ok && len(rawThemes) > 0 {
		_ = json.Unmarshal(rawThemes, &themes)
	}
	if len(themes) > ksiThemeCount {
		themes = themes[:ksiThemeCount]
	}
	for len(themes) < ksiThemeCount {
		themes = append(themes, map[string]json.RawMessage{})
	}
	for i := range themes {
		if themes[i] == nil {
			themes[i] = map[string]json.RawMessage{}
		}
		var answers [][]string
		if rawAnswers, ok := themes[i]["answers"]; ok && len(rawAnswers) > 0 {
			_ = json.Unmarshal(rawAnswers, &answers)
		}
		answers = resizeStringMatrix(answers, len(participants), len(questionValues))
		answersJSON, err := json.Marshal(answers)
		if err != nil {
			return nil, err
		}
		themes[i]["answers"] = answersJSON
	}
	themesJSON, err := json.Marshal(themes)
	if err != nil {
		return nil, err
	}
	obj["themes"] = themesJSON
	return json.Marshal(obj)
}

func rawJSONObject(raw string) (map[string]json.RawMessage, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return map[string]json.RawMessage{}, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		obj = map[string]json.RawMessage{}
	}
	return obj, nil
}

func chgkTeamsFromRoster(teams []festRosterImportTeam) []chgkTeamJSON {
	out := make([]chgkTeamJSON, 0, len(teams))
	for _, team := range teams {
		out = append(out, chgkTeamJSON{Name: team.Name, City: team.City})
	}
	return out
}

func teamNamesFromRoster(teams []festRosterImportTeam) []string {
	out := make([]string, 0, len(teams))
	for _, team := range teams {
		out = append(out, team.Name)
	}
	return out
}

func resizeIntSlice(values []int, size int) []int {
	if len(values) > size {
		return values[:size]
	}
	out := append([]int(nil), values...)
	for len(out) < size {
		out = append(out, 0)
	}
	return out
}

func resizeStringMatrix(values [][]string, rows, cols int) [][]string {
	if len(values) > rows {
		values = values[:rows]
	}
	out := make([][]string, rows)
	for row := 0; row < rows; row++ {
		if row < len(values) {
			out[row] = resizeStringSlice(values[row], cols)
		} else {
			out[row] = make([]string, cols)
		}
	}
	return out
}

func resizeStringSlice(values []string, size int) []string {
	if len(values) > size {
		return values[:size]
	}
	out := append([]string(nil), values...)
	for len(out) < size {
		out = append(out, "")
	}
	return out
}
