package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
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
	Number   int64
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

// chgkTeamJSON is one team in an OD game's state. OD keys scores by team NUMBER,
// not by position: each cell of state.entries stores the team's Number, and the
// teams array is only a number→name/city lookup. Number is the universal team
// identity (shared with KSI/EK) and is guaranteed present for active teams, so
// re-import reorders never misattribute scores — the entries stay valid as long
// as a team keeps its number (sticky across re-import).
type chgkTeamJSON struct {
	Name   string `json:"name"`
	City   string `json:"city,omitempty"`
	Number int64  `json:"number,omitempty"`
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
	return sortedFestRosterImportTeams(teams), nil
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
	teams = sortedFestRosterImportTeams(teams)

	var updates []gameStateBroadcast
	var ekOverrideGameIDs []int64
	var revision int64
	result, err := func() (ratingRosterImportResult, error) {
		s.mu.Lock()
		defer s.mu.Unlock()

		tx, err := s.beginWriteTx(ctx)
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

		existingByRating, maxSeenNumber, err := loadFestExistingTeams(ctx, tx, festID)
		if err != nil {
			return ratingRosterImportResult{}, err
		}
		preservedOverrides, err := loadRatingPlayerTeamOverrides(ctx, tx, festID)
		if err != nil {
			return ratingRosterImportResult{}, err
		}
		assignFestNumbersForImport(teams, existingByRating, maxSeenNumber)

		// Soft-delete fest_teams whose rating_id is not in the incoming roster.
		// Their numbers stay in the row so they reappear if the team returns.
		incomingRatingIDs := make(map[int64]struct{}, len(teams))
		for _, team := range teams {
			if team.RatingID > 0 {
				incomingRatingIDs[team.RatingID] = struct{}{}
			}
		}
		for ratingID, existing := range existingByRating {
			if _, stays := incomingRatingIDs[ratingID]; stays {
				continue
			}
			if _, err := tx.ExecContext(ctx, `update fest_teams set deleted = 1 where id = ?`, existing.ID); err != nil {
				return ratingRosterImportResult{}, err
			}
		}
		// Hard-delete rows that don't have a rating_id — we can't match them
		// across re-syncs anyway, and they have no archived numbers worth keeping.
		if _, err := tx.ExecContext(ctx, `delete from fest_teams where fest_id = ? and rating_id is null`, festID); err != nil {
			return ratingRosterImportResult{}, err
		}
		// Players are fully rebuilt on every import.
		if _, err := tx.ExecContext(ctx, `delete from fest_team_players where team_id in (select id from fest_teams where fest_id = ?)`, festID); err != nil {
			return ratingRosterImportResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `delete from fest_players where fest_id = ?`, festID); err != nil {
			return ratingRosterImportResult{}, err
		}

		playerIDs := make(map[string]int64)
		playerCount := 0
		for fallbackPosition, team := range teams {
			importOrder := fallbackPosition + 1
			var numberParam any
			if team.Number > 0 {
				numberParam = team.Number
			}
			var teamID int64
			if existing, ok := existingByRating[team.RatingID]; ok && team.RatingID > 0 {
				teamID = existing.ID
				if _, err := tx.ExecContext(ctx, `
update fest_teams
   set name = ?, city = ?, position = ?, number = ?, deleted = 0
 where id = ?`, team.Name, team.City, importOrder, numberParam, teamID); err != nil {
					return ratingRosterImportResult{}, err
				}
			} else {
				var err error
				teamID, err = insertReturningID(ctx, tx, `
insert into fest_teams(fest_id, rating_id, name, city, position, number, deleted)
values(?, ?, ?, ?, ?, ?, 0)`, festID, nullableInt64(team.RatingID), team.Name, team.City, importOrder, numberParam)
				if err != nil {
					return ratingRosterImportResult{}, err
				}
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

		chgkUpdates, err := propagateRosterToChGKTx(ctx, tx, festID, teams, nil)
		if err != nil {
			return ratingRosterImportResult{}, err
		}
		ksiUpdates, err := propagateRosterToKSITx(ctx, tx, festID, teams)
		if err != nil {
			return ratingRosterImportResult{}, err
		}
		updates = append(chgkUpdates, ksiUpdates...)
		ekOverrideGameIDs, err = restoreRatingPlayerTeamOverridesTx(ctx, tx, festID, preservedOverrides)
		if err != nil {
			return ratingRosterImportResult{}, err
		}
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
	for _, gameID := range ekOverrideGameIDs {
		s.broadcastState(festID, fmt.Sprintf("game-roster:%d", gameID), revision, []byte(`{}`))
	}
	return result, nil
}

type existingFestTeam struct {
	ID     int64
	Number int64
}

// loadFestExistingTeams returns the rating_id → row mapping for every fest_team
// in this fest (including soft-deleted ones, so that previously archived
// numbers can be restored when a team is re-added). maxSeenNumber is the
// largest number ever assigned in this fest — new teams introduced by a
// re-sync always receive numbers strictly greater than this, so already-printed
// answer sheets keep referring to the right team.
func loadFestExistingTeams(ctx context.Context, tx *sql.Tx, festID int64) (map[int64]existingFestTeam, int64, error) {
	rows, err := tx.QueryContext(ctx, `
select id, coalesce(rating_id, 0), coalesce(number, 0)
from fest_teams
where fest_id = ?`, festID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	byRating := make(map[int64]existingFestTeam)
	var maxNum int64
	for rows.Next() {
		var id, ratingID, number int64
		if err := rows.Scan(&id, &ratingID, &number); err != nil {
			return nil, 0, err
		}
		if ratingID > 0 {
			byRating[ratingID] = existingFestTeam{ID: id, Number: number}
		}
		if number > maxNum {
			maxNum = number
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return byRating, maxNum, nil
}

// assignFestNumbersForImport mutates teams in place so that:
//   - teams that already had a number (matched by rating_id, including
//     previously soft-deleted ones) keep it;
//   - every still-unnumbered team receives a fresh number strictly greater than
//     the largest one ever seen in this fest, in the (alphabetical) order of
//     incoming teams.
//
// Team number is the universal team identity across OD/KSI/EK (see the
// team-number unification), so every active team must always have one. On a
// first-ever import maxSeen is 0 and numbering starts at 1; on re-import,
// rating-matched teams keep their numbers and new teams continue past maxSeen
// (which counts soft-deleted rows too, so a returning team can't collide).
func assignFestNumbersForImport(teams []festRosterImportTeam, existing map[int64]existingFestTeam, maxSeen int64) {
	for i := range teams {
		teams[i].Number = 0
		if teams[i].RatingID > 0 {
			if e, ok := existing[teams[i].RatingID]; ok {
				teams[i].Number = e.Number
			}
		}
	}
	next := maxSeen + 1
	for i := range teams {
		if teams[i].Number == 0 {
			teams[i].Number = next
			next++
		}
	}
}

func sortedFestRosterImportTeams(teams []festRosterImportTeam) []festRosterImportTeam {
	out := make([]festRosterImportTeam, len(teams))
	for i, team := range teams {
		out[i] = team
		out[i].Players = append([]festRosterImportPlayer(nil), team.Players...)
		sort.SliceStable(out[i].Players, func(a, b int) bool {
			return compareAlpha(importPlayerName(out[i].Players[a]), importPlayerName(out[i].Players[b])) < 0
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if cmp := compareAlpha(out[i].Name, out[j].Name); cmp != 0 {
			return cmp < 0
		}
		if cmp := compareAlpha(out[i].City, out[j].City); cmp != 0 {
			return cmp < 0
		}
		return out[i].RatingID < out[j].RatingID
	})
	return out
}

func importPlayerName(player festRosterImportPlayer) string {
	return joinPlayerName(player.FirstName, player.LastName)
}

func compareAlpha(a, b string) int {
	ak := alphaKey(a)
	bk := alphaKey(b)
	if ak < bk {
		return -1
	}
	if ak > bk {
		return 1
	}
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func alphaKey(value string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "ё", "е")
}

func rosterPlayerKey(player festRosterImportPlayer) string {
	if player.RatingID > 0 {
		return "rating:" + strconv.FormatInt(player.RatingID, 10)
	}
	return "name:" + strings.ToLower(joinPlayerName(player.FirstName, player.LastName))
}

func propagateRosterToChGKTx(ctx context.Context, tx *sql.Tx, festID int64, teams []festRosterImportTeam, entryRemap map[int]int) ([]gameStateBroadcast, error) {
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
		stateJSON, err := applyRosterToChGKState(game.State, teams, entryRemap)
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
		stateJSON, err := applyRosterToKSIState(game.State, teams, ksiThemeCountFromSchemeJSON(game.Scheme))
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

// applyRosterToChGKState refreshes an OD game's teams lookup and resizes its
// entries grid for the new roster. Entries hold team NUMBERS as values (the
// universal identity), so a roster reorder needs no per-cell remap — only an
// explicit number reassignment does (entryRemap, supplied by saveFestNumbers,
// nil on plain re-import). See chgkTeamJSON.
func applyRosterToChGKState(raw string, teams []festRosterImportTeam, entryRemap map[int]int) ([]byte, error) {
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
				if len(entryRemap) > 0 {
					for j, value := range entries[i] {
						if mapped, ok := entryRemap[value]; ok {
							entries[i][j] = mapped
						}
					}
				}
			}
			entriesJSON, err := json.Marshal(entries)
			if err != nil {
				return nil, err
			}
			obj["entries"] = entriesJSON
		}
	}
	if len(entryRemap) > 0 {
		if rawRounds, ok := obj["shootoutRounds"]; ok && len(rawRounds) > 0 {
			if roundsJSON, err := remapChGKShootoutRounds(rawRounds, entryRemap); err == nil {
				obj["shootoutRounds"] = roundsJSON
			}
		}
	}
	delete(obj, "answers")
	delete(obj, "finished")
	return json.Marshal(obj)
}

type chgkShootoutRoundJSON struct {
	Teams     []int      `json:"teams"`
	Entries   [][]int    `json:"entries,omitempty"`
	Completed []bool     `json:"completed,omitempty"`
	Answers   [][]string `json:"answers"`
}

func remapChGKShootoutRounds(raw json.RawMessage, entryRemap map[int]int) (json.RawMessage, error) {
	var rounds []chgkShootoutRoundJSON
	if err := json.Unmarshal(raw, &rounds); err != nil {
		return nil, err
	}
	for roundIndex := range rounds {
		for teamIndex, number := range rounds[roundIndex].Teams {
			if mapped, ok := entryRemap[number]; ok {
				rounds[roundIndex].Teams[teamIndex] = mapped
			}
		}
		for questionIndex := range rounds[roundIndex].Entries {
			for slot, number := range rounds[roundIndex].Entries[questionIndex] {
				if mapped, ok := entryRemap[number]; ok {
					rounds[roundIndex].Entries[questionIndex][slot] = mapped
				}
			}
		}
	}
	return json.Marshal(rounds)
}

func applyRosterToKSIScheme(raw string, teams []festRosterImportTeam) ([]byte, error) {
	obj, err := rawJSONObject(raw)
	if err != nil {
		return nil, err
	}
	themesCount := ksiThemeCount
	if rawThemes, ok := obj["themes"]; ok && len(rawThemes) > 0 {
		var configured int
		if err := json.Unmarshal(rawThemes, &configured); err == nil && configured > 0 {
			themesCount = configured
		}
	}
	participantsJSON, err := json.Marshal(teamParticipantsFromRoster(teams))
	if err != nil {
		return nil, err
	}
	gameTypeJSON, err := json.Marshal("ksi")
	if err != nil {
		return nil, err
	}
	themesJSON, err := json.Marshal(themesCount)
	if err != nil {
		return nil, err
	}
	obj["gameType"] = gameTypeJSON
	obj["participants"] = participantsJSON
	obj["themes"] = themesJSON
	return json.Marshal(obj)
}

func applyRosterToKSIState(raw string, teams []festRosterImportTeam, targetThemeCount int) ([]byte, error) {
	obj, err := rawJSONObject(raw)
	if err != nil {
		return nil, err
	}
	// Capture the pre-import participant order before overwriting it, so the
	// answer grid (keyed by row position) can be remapped to follow each team
	// across roster reorders/additions/removals instead of staying at its old
	// index. Read tolerantly: new states store [{number,name}], legacy states a
	// bare name array (matched by name for that one transition).
	oldParticipants := parseKSIParticipants(obj["participants"])
	participants := teamParticipantsFromRoster(teams)
	participantsJSON, err := json.Marshal(participants)
	if err != nil {
		return nil, err
	}
	obj["participants"] = participantsJSON

	var themes []map[string]json.RawMessage
	if rawThemes, ok := obj["themes"]; ok && len(rawThemes) > 0 {
		_ = json.Unmarshal(rawThemes, &themes)
	}
	if targetThemeCount <= 0 {
		targetThemeCount = len(themes)
	}
	if targetThemeCount <= 0 {
		targetThemeCount = ksiThemeCount
	}
	if len(themes) > targetThemeCount {
		themes = themes[:targetThemeCount]
	}
	for len(themes) < targetThemeCount {
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
		answers = remapAnswerMatrix(answers, oldParticipants, participants, len(questionValues))
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

func ksiThemeCountFromSchemeJSON(raw string) int {
	obj, err := rawJSONObject(raw)
	if err != nil {
		return 0
	}
	if rawThemes, ok := obj["themes"]; ok && len(rawThemes) > 0 {
		var themesCount int
		if err := json.Unmarshal(rawThemes, &themesCount); err == nil && themesCount > 0 {
			return themesCount
		}
	}
	return 0
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
		out = append(out, chgkTeamJSON{Name: team.Name, City: team.City, Number: team.Number})
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

// ksiParticipant is one row of a KSI game's participants list. Number is the
// team's universal identity (the join key for the answer-grid remap); Name is
// shown in the UI. Stored as objects [{number,name}]; legacy states stored a
// bare name array and are read tolerantly via parseKSIParticipants.
type ksiParticipant struct {
	Number int    `json:"number"`
	Name   string `json:"name"`
}

func teamParticipantsFromRoster(teams []festRosterImportTeam) []ksiParticipant {
	out := make([]ksiParticipant, 0, len(teams))
	for _, team := range teams {
		out = append(out, ksiParticipant{Number: int(team.Number), Name: team.Name})
	}
	return out
}

// parseKSIParticipants decodes a participants array tolerating both the current
// [{number,name}] shape and the legacy ["name", ...] shape (so existing game
// states keep loading during/after the migration).
func parseKSIParticipants(raw json.RawMessage) []ksiParticipant {
	if len(raw) == 0 {
		return nil
	}
	var objs []ksiParticipant
	if err := json.Unmarshal(raw, &objs); err == nil {
		return objs
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err == nil {
		out := make([]ksiParticipant, len(names))
		for i, name := range names {
			out[i] = ksiParticipant{Name: name}
		}
		return out
	}
	return nil
}

// participantNames projects the name column out of a participants list.
func participantNames(parts []ksiParticipant) []string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = p.Name
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

// remapAnswerMatrix rebuilds a KSI answer grid for a new participant order,
// moving each old row to wherever its team now sits so scores follow their team
// across roster reorders, additions, and removals. Teams are matched by NUMBER
// (the universal, unique identity) — so two teams sharing a name keep distinct
// scores — falling back to name only when the old participant has no number
// (legacy state captured before numbers were stored). New teams get an empty
// row; teams that dropped out lose their row. Each old row is claimed at most
// once. With no old participants at all, a plain positional resize is used.
func remapAnswerMatrix(values [][]string, oldParts, newParts []ksiParticipant, cols int) [][]string {
	if len(oldParts) == 0 {
		return resizeStringMatrix(values, len(newParts), cols)
	}
	consumed := make([]bool, len(oldParts))
	claim := func(match func(ksiParticipant) bool) int {
		for i, p := range oldParts {
			if !consumed[i] && match(p) {
				consumed[i] = true
				return i
			}
		}
		return -1
	}
	out := make([][]string, len(newParts))
	for j, p := range newParts {
		idx := -1
		if p.Number > 0 {
			num := p.Number
			idx = claim(func(o ksiParticipant) bool { return o.Number == num })
		}
		if idx < 0 && p.Name != "" {
			name := p.Name
			idx = claim(func(o ksiParticipant) bool { return o.Name == name })
		}
		var srcRow []string
		if idx >= 0 && idx < len(values) {
			srcRow = values[idx]
		}
		out[j] = resizeStringSlice(srcRow, cols)
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
