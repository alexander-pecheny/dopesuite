package dopeserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"dope/dope/games"
	"dope/dope/store"
)

type hostPlayerOverrideOption struct {
	ID    int64
	Label string
}

type hostTeamOverrideOption struct {
	ID    int64
	Label string
}

type hostGameOverrideOption struct {
	ID    int64
	Label string
}

type hostPlayerOverrideRow struct {
	PlayerID       int64
	SourceTeamID   int64
	OverrideTeamID int64
	Player         string
	SourceTeam     string
	OverrideTeam   string
	Games          string
	GameIDs        []int64
	games          []string
}

func (r hostPlayerOverrideRow) HasGame(gameID int64) bool {
	for _, id := range r.GameIDs {
		if id == gameID {
			return true
		}
	}
	return false
}

func (r hostPlayerOverrideRow) DialogID() string {
	return fmt.Sprintf("playerOverrideEdit-%d-%d-%d", r.PlayerID, r.SourceTeamID, r.OverrideTeamID)
}

type rosterOverrideTeam struct {
	FestTeamID int64
	Name       string
	City       string
	Players    []rosterOverridePlayer
}

type rosterOverridePlayer struct {
	FestPlayerID int64
	FirstName    string
	LastName     string
}

type ratingPlayerTeamOverride struct {
	GameID             int64
	PlayerRatingID     int64
	OverrideTeamRating int64
}

func (s *server) loadHostPlayerOverrideOptions(ctx context.Context, festID int64) ([]hostPlayerOverrideOption, []hostTeamOverrideOption, []hostGameOverrideOption, []hostPlayerOverrideRow, error) {
	players, err := loadHostPlayerOverridePlayerOptions(ctx, s.db, festID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	teams, err := loadHostPlayerOverrideTeamOptions(ctx, s.db, festID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	games, err := loadHostPlayerOverrideGameOptions(ctx, s.db, festID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	rows, err := loadHostPlayerOverrideRows(ctx, s.db, festID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return players, teams, games, rows, nil
}

func loadHostPlayerOverridePlayerOptions(ctx context.Context, q store.Queryer, festID int64) ([]hostPlayerOverrideOption, error) {
	return store.CollectRows(ctx, q, `
select p.id, coalesce(p.rating_id, 0), p.first_name, p.last_name, tt.name
from fest_team_players ttp
join fest_players p on p.id = ttp.player_id
join fest_teams tt on tt.id = ttp.team_id
where p.fest_id = ? and tt.fest_id = ? and tt.deleted = 0
order by p.last_name, p.first_name, p.id`, []any{festID, festID}, func(rows *sql.Rows) (hostPlayerOverrideOption, error) {
		var id, ratingID int64
		var firstName, lastName, teamName string
		if err := rows.Scan(&id, &ratingID, &firstName, &lastName, &teamName); err != nil {
			return hostPlayerOverrideOption{}, err
		}
		name := store.JoinPlayerName(firstName, lastName)
		label := fmt.Sprintf("%s - %s", name, teamName)
		if ratingID > 0 {
			label = fmt.Sprintf("%s - %s - rating %d", name, teamName, ratingID)
		}
		return hostPlayerOverrideOption{ID: id, Label: label}, nil
	})
}

func loadHostPlayerOverrideTeamOptions(ctx context.Context, q store.Queryer, festID int64) ([]hostTeamOverrideOption, error) {
	return store.CollectRows(ctx, q, `
select id, coalesce(rating_id, 0), name, city
from fest_teams
where fest_id = ? and deleted = 0
order by name, city, id`, []any{festID}, func(rows *sql.Rows) (hostTeamOverrideOption, error) {
		var id, ratingID int64
		var name, city string
		if err := rows.Scan(&id, &ratingID, &name, &city); err != nil {
			return hostTeamOverrideOption{}, err
		}
		label := name
		if city != "" {
			label += " (" + city + ")"
		}
		if ratingID > 0 {
			label += fmt.Sprintf(" - rating %d", ratingID)
		}
		return hostTeamOverrideOption{ID: id, Label: label}, nil
	})
}

func loadHostPlayerOverrideGameOptions(ctx context.Context, q store.Queryer, festID int64) ([]hostGameOverrideOption, error) {
	return store.CollectRows(ctx, q, `
select id, title, game_type
from games
where fest_id = ? and game_type in ('ksi', 'ek')
order by position, id`, []any{festID}, func(rows *sql.Rows) (hostGameOverrideOption, error) {
		var row hostGameOverrideOption
		var title, gameType string
		if err := rows.Scan(&row.ID, &title, &gameType); err != nil {
			return row, err
		}
		row.Label = overrideGameLabel(title, gameType)
		return row, nil
	})
}

func loadHostPlayerOverrideRows(ctx context.Context, q store.Queryer, festID int64) ([]hostPlayerOverrideRow, error) {
	rows, err := q.QueryContext(ctx, `
select p.id, p.first_name, p.last_name, source.id, source.name, target.id, target.name, g.id, g.title, g.game_type
from game_player_team_overrides o
join fest_players p on p.id = o.player_id
join fest_teams source on source.id = o.source_team_id
join fest_teams target on target.id = o.override_team_id
join games g on g.id = o.game_id
where o.fest_id = ?
order by p.last_name, p.first_name, source.name, target.name, g.position, g.id`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type groupKey struct {
		playerID int64
		sourceID int64
		targetID int64
	}
	index := make(map[groupKey]int)
	var out []hostPlayerOverrideRow
	for rows.Next() {
		var firstName, lastName, gameType string
		var playerID, sourceID, targetID, rowGameID int64
		var gameTitle string
		var row hostPlayerOverrideRow
		if err := rows.Scan(&playerID, &firstName, &lastName, &sourceID, &row.SourceTeam, &targetID, &row.OverrideTeam, &rowGameID, &gameTitle, &gameType); err != nil {
			return nil, err
		}
		row.Player = store.JoinPlayerName(firstName, lastName)
		key := groupKey{playerID: playerID, sourceID: sourceID, targetID: targetID}
		i, ok := index[key]
		if !ok {
			i = len(out)
			index[key] = i
			row.PlayerID = playerID
			row.SourceTeamID = sourceID
			row.OverrideTeamID = targetID
			out = append(out, row)
		}
		label := overrideGameLabel(gameTitle, gameType)
		if !containsString(out[i].games, label) {
			out[i].games = append(out[i].games, label)
			out[i].Games = strings.Join(out[i].games, ", ")
		}
		if !containsInt64(out[i].GameIDs, rowGameID) {
			out[i].GameIDs = append(out[i].GameIDs, rowGameID)
		}
	}
	return out, rows.Err()
}

func overrideGameLabel(title, gameType string) string {
	title = strings.TrimSpace(title)
	typeLabel := games.Label(gameType)
	if title == "" || strings.EqualFold(title, typeLabel) {
		return typeLabel
	}
	return title + " · " + typeLabel
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func containsInt64(values []int64, needle int64) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func (s *server) savePlayerTeamOverride(reqCtx context.Context, festID, playerID, overrideTeamID int64, gameIDs []int64) (int64, []int64, error) {
	if playerID <= 0 {
		return 0, nil, errors.New("Выберите игрока из подсказки.")
	}
	if overrideTeamID <= 0 {
		return 0, nil, errors.New("Выберите новую команду из подсказки.")
	}
	if len(gameIDs) == 0 {
		return 0, nil, errors.New("Выберите хотя бы одну игру.")
	}

	var revision int64
	var ekGameIDs []int64
	err := s.withWriteTx(reqCtx, festID, "player-override-save", func(ctx context.Context, tx *sql.Tx) error {
		sourceTeamID, err := sourceTeamForFestPlayer(ctx, tx, festID, playerID)
		if err != nil {
			return err
		}
		if sourceTeamID == overrideTeamID {
			return errors.New("Новая команда совпадает с командой игрока в рейтинге.")
		}
		if err := assertActiveFestTeam(ctx, tx, festID, overrideTeamID); err != nil {
			return err
		}

		now := utcNow()
		gameTypes := make(map[int64]string, len(gameIDs))
		for _, gameID := range uniqueInt64s(gameIDs) {
			gameType, err := overrideGameType(ctx, tx, festID, gameID)
			if err != nil {
				return err
			}
			gameTypes[gameID] = gameType
			if _, err := tx.ExecContext(ctx, `
insert into game_player_team_overrides(fest_id, game_id, player_id, source_team_id, override_team_id, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?)
on conflict(fest_id, game_id, player_id) do update set
  source_team_id = excluded.source_team_id,
  override_team_id = excluded.override_team_id,
  updated_at = excluded.updated_at`,
				festID, gameID, playerID, sourceTeamID, overrideTeamID, now, now); err != nil {
				return err
			}
		}

		ekGameIDs = nil
		for gameID, gameType := range gameTypes {
			if _, err := loadFestRosterWithGameOverrides(ctx, tx, festID, gameID); err != nil {
				return err
			}
			if gameType != "ek" {
				continue
			}
			if err := materializeGameRosterOverridesTx(ctx, tx, festID, gameID); err != nil {
				return err
			}
			ekGameIDs = append(ekGameIDs, gameID)
		}
		sort.Slice(ekGameIDs, func(i, j int) bool { return ekGameIDs[i] < ekGameIDs[j] })

		revision, err = bumpFestRevisionTx(ctx, tx, festID, "roster:player-override", mustJSON(map[string]any{
			"playerID":       playerID,
			"sourceTeamID":   sourceTeamID,
			"overrideTeamID": overrideTeamID,
			"games":          len(gameTypes),
		}))
		return err
	})
	if err != nil {
		return 0, nil, err
	}
	return revision, ekGameIDs, nil
}

func (s *server) replacePlayerTeamOverride(reqCtx context.Context, festID, playerID, sourceTeamID, overrideTeamID int64, gameIDs []int64) (int64, []int64, error) {
	if playerID <= 0 || sourceTeamID <= 0 || overrideTeamID <= 0 {
		return 0, nil, errors.New("Оверрайд не найден.")
	}

	var revision int64
	var ekGameIDs []int64
	err := s.withWriteTx(reqCtx, festID, "player-override-replace", func(ctx context.Context, tx *sql.Tx) error {
		currentSourceTeamID, err := sourceTeamForFestPlayer(ctx, tx, festID, playerID)
		if err != nil {
			return err
		}
		if err := assertActiveFestTeam(ctx, tx, festID, overrideTeamID); err != nil {
			return err
		}

		affectedGameTypes := make(map[int64]string)
		existingRows, err := tx.QueryContext(ctx, `
select o.game_id, g.game_type
from game_player_team_overrides o
join games g on g.id = o.game_id
where o.fest_id = ? and o.player_id = ? and o.source_team_id = ? and o.override_team_id = ?`,
			festID, playerID, sourceTeamID, overrideTeamID)
		if err != nil {
			return err
		}
		for existingRows.Next() {
			var gameID int64
			var gameType string
			if err := existingRows.Scan(&gameID, &gameType); err != nil {
				existingRows.Close()
				return err
			}
			affectedGameTypes[gameID] = gameType
		}
		if err := existingRows.Close(); err != nil {
			return err
		}
		if err := existingRows.Err(); err != nil {
			return err
		}
		if len(affectedGameTypes) == 0 {
			return errors.New("Оверрайд не найден.")
		}

		newGameTypes := make(map[int64]string)
		for _, gameID := range uniqueInt64s(gameIDs) {
			gameType, err := overrideGameType(ctx, tx, festID, gameID)
			if err != nil {
				return err
			}
			newGameTypes[gameID] = gameType
			affectedGameTypes[gameID] = gameType
		}

		if _, err := tx.ExecContext(ctx, `
delete from game_player_team_overrides
where fest_id = ? and player_id = ? and source_team_id = ? and override_team_id = ?`,
			festID, playerID, sourceTeamID, overrideTeamID); err != nil {
			return err
		}

		now := utcNow()
		for gameID := range newGameTypes {
			if _, err := tx.ExecContext(ctx, `
insert into game_player_team_overrides(fest_id, game_id, player_id, source_team_id, override_team_id, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?)`,
				festID, gameID, playerID, currentSourceTeamID, overrideTeamID, now, now); err != nil {
				return err
			}
		}

		ekGameIDs = nil
		for gameID, gameType := range affectedGameTypes {
			if _, err := loadFestRosterWithGameOverrides(ctx, tx, festID, gameID); err != nil {
				return err
			}
			if gameType != "ek" {
				continue
			}
			if err := materializeGameRosterOverridesTx(ctx, tx, festID, gameID); err != nil {
				return err
			}
			ekGameIDs = append(ekGameIDs, gameID)
		}
		sort.Slice(ekGameIDs, func(i, j int) bool { return ekGameIDs[i] < ekGameIDs[j] })

		revision, err = bumpFestRevisionTx(ctx, tx, festID, "roster:player-override-edit", mustJSON(map[string]any{
			"playerID":       playerID,
			"sourceTeamID":   sourceTeamID,
			"overrideTeamID": overrideTeamID,
			"games":          len(newGameTypes),
		}))
		return err
	})
	if err != nil {
		return 0, nil, err
	}
	return revision, ekGameIDs, nil
}

func parseHostOverrideID(raw, label string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("Выберите %s из подсказки.", label)
	}
	return id, nil
}

func parseHostOverrideGameIDs(values []string) ([]int64, error) {
	var out []int64
	for _, raw := range values {
		id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("Некорректная игра.")
		}
		out = append(out, id)
	}
	return uniqueInt64s(out), nil
}

func uniqueInt64s(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func sourceTeamForFestPlayer(ctx context.Context, q store.Queryer, festID, playerID int64) (int64, error) {
	var teamID int64
	err := q.QueryRowContext(ctx, `
select ttp.team_id
from fest_team_players ttp
join fest_players p on p.id = ttp.player_id
join fest_teams tt on tt.id = ttp.team_id
where p.id = ? and p.fest_id = ? and tt.fest_id = ? and tt.deleted = 0
order by ttp.roster_order, tt.position, tt.id
limit 1`, playerID, festID, festID).Scan(&teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errors.New("Игрок не найден в активном составе феста.")
	}
	return teamID, err
}

func assertActiveFestTeam(ctx context.Context, q store.Queryer, festID, teamID int64) error {
	var found int64
	err := q.QueryRowContext(ctx, `
select id from fest_teams where id = ? and fest_id = ? and deleted = 0`, teamID, festID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("Команда не найдена в активном составе феста.")
	}
	return err
}

func overrideGameType(ctx context.Context, q store.Queryer, festID, gameID int64) (string, error) {
	var gameType string
	err := q.QueryRowContext(ctx, `
select game_type from games where id = ? and fest_id = ? and game_type in ('ksi', 'ek')`, gameID, festID).Scan(&gameType)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("Игра для оверрайда должна быть КСИ или ЭК.")
	}
	return gameType, err
}

func gameHasPlayerOverridesTx(ctx context.Context, q store.Queryer, festID, gameID int64) (bool, error) {
	var count int
	if err := q.QueryRowContext(ctx, `
select count(*) from game_player_team_overrides where fest_id = ? and game_id = ?`, festID, gameID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func loadFestRosterWithGameOverrides(ctx context.Context, q store.Queryer, festID, gameID int64) ([]rosterOverrideTeam, error) {
	teams, err := loadBaseFestRosterForOverride(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	byTeamID := make(map[int64]*rosterOverrideTeam, len(teams))
	for i := range teams {
		byTeamID[teams[i].FestTeamID] = &teams[i]
	}

	rows, err := q.QueryContext(ctx, `
select player_id, source_team_id, override_team_id
from game_player_team_overrides
where fest_id = ? and game_id = ?
order by player_id`, festID, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var playerID, sourceTeamID, targetTeamID int64
		if err := rows.Scan(&playerID, &sourceTeamID, &targetTeamID); err != nil {
			return nil, err
		}
		target := byTeamID[targetTeamID]
		if target == nil {
			continue
		}
		player, ok := removeOverridePlayer(teams, sourceTeamID, playerID)
		if !ok {
			player, ok = removeOverridePlayer(teams, 0, playerID)
		}
		if !ok || containsOverridePlayer(target.Players, playerID) {
			continue
		}
		target.Players = append(target.Players, player)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, team := range teams {
		if len(team.Players) > 9 {
			return nil, fmt.Errorf("В команде %q после оверрайдов больше 9 игроков.", team.Name)
		}
	}
	return teams, nil
}

func loadBaseFestRosterForOverride(ctx context.Context, q store.Queryer, festID int64) ([]rosterOverrideTeam, error) {
	teams, err := store.CollectRows(ctx, q, `
select id, name, city
from fest_teams
where fest_id = ? and deleted = 0
order by position, id`, []any{festID}, func(rows *sql.Rows) (rosterOverrideTeam, error) {
		var team rosterOverrideTeam
		if err := rows.Scan(&team.FestTeamID, &team.Name, &team.City); err != nil {
			return team, err
		}
		return team, nil
	})
	if err != nil {
		return nil, err
	}
	for i := range teams {
		players, err := loadFestRosterOverridePlayers(ctx, q, teams[i].FestTeamID)
		if err != nil {
			return nil, err
		}
		teams[i].Players = players
	}
	return teams, nil
}

func loadFestRosterOverridePlayers(ctx context.Context, q store.Queryer, festTeamID int64) ([]rosterOverridePlayer, error) {
	return store.CollectRows(ctx, q, `
select p.id, p.first_name, p.last_name
from fest_team_players ftp
join fest_players p on p.id = ftp.player_id
where ftp.team_id = ?
order by ftp.roster_order, p.id`, []any{festTeamID}, func(rows *sql.Rows) (rosterOverridePlayer, error) {
		var player rosterOverridePlayer
		if err := rows.Scan(&player.FestPlayerID, &player.FirstName, &player.LastName); err != nil {
			return player, err
		}
		return player, nil
	})
}

func removeOverridePlayer(teams []rosterOverrideTeam, preferredTeamID, playerID int64) (rosterOverridePlayer, bool) {
	for i := range teams {
		if preferredTeamID > 0 && teams[i].FestTeamID != preferredTeamID {
			continue
		}
		for j, player := range teams[i].Players {
			if player.FestPlayerID != playerID {
				continue
			}
			teams[i].Players = append(teams[i].Players[:j], teams[i].Players[j+1:]...)
			return player, true
		}
	}
	return rosterOverridePlayer{}, false
}

func containsOverridePlayer(players []rosterOverridePlayer, playerID int64) bool {
	for _, player := range players {
		if player.FestPlayerID == playerID {
			return true
		}
	}
	return false
}

func materializeGameRosterOverridesTx(ctx context.Context, tx *sql.Tx, festID, gameID int64) error {
	teams, err := loadFestRosterWithGameOverrides(ctx, tx, festID, gameID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from game_team_players where game_id = ?`, gameID); err != nil {
		return err
	}
	for _, team := range teams {
		teamID, _, err := ensureSeedTeam(ctx, tx, festID, team.Name, team.City, nil)
		if err != nil {
			return err
		}
		for rosterOrder, player := range team.Players {
			playerID, err := ensureSeedPlayer(ctx, tx, festID, strings.TrimSpace(player.FirstName), strings.TrimSpace(player.LastName))
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
insert into game_team_players(game_id, team_id, player_id, roster_order)
values(?, ?, ?, ?)`, gameID, teamID, playerID, rosterOrder); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `update games set roster_source = 'game', updated_at = ? where id = ? and fest_id = ?`, utcNow(), gameID, festID)
	return err
}

func loadRatingPlayerTeamOverrides(ctx context.Context, q store.Queryer, festID int64) ([]ratingPlayerTeamOverride, error) {
	rows, err := q.QueryContext(ctx, `
select o.game_id, coalesce(p.rating_id, 0), coalesce(target.rating_id, 0)
from game_player_team_overrides o
join fest_players p on p.id = o.player_id
join fest_teams target on target.id = o.override_team_id
join games g on g.id = o.game_id
where o.fest_id = ? and g.game_type in ('ksi', 'ek')
order by o.game_id, o.player_id`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ratingPlayerTeamOverride
	for rows.Next() {
		var row ratingPlayerTeamOverride
		if err := rows.Scan(&row.GameID, &row.PlayerRatingID, &row.OverrideTeamRating); err != nil {
			return nil, err
		}
		if row.PlayerRatingID <= 0 || row.OverrideTeamRating <= 0 {
			continue
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func restoreRatingPlayerTeamOverridesTx(ctx context.Context, tx *sql.Tx, festID int64, overrides []ratingPlayerTeamOverride) ([]int64, error) {
	if len(overrides) == 0 {
		return nil, nil
	}
	now := utcNow()
	ekGames := make(map[int64]struct{})
	for _, override := range overrides {
		playerID, ok, err := festPlayerIDByRating(ctx, tx, festID, override.PlayerRatingID)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		overrideTeamID, ok, err := activeFestTeamIDByRating(ctx, tx, festID, override.OverrideTeamRating)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		sourceTeamID, err := sourceTeamForFestPlayer(ctx, tx, festID, playerID)
		if err != nil {
			return nil, err
		}
		if sourceTeamID == overrideTeamID {
			continue
		}
		gameType, err := overrideGameType(ctx, tx, festID, override.GameID)
		if err != nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
insert into game_player_team_overrides(fest_id, game_id, player_id, source_team_id, override_team_id, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?)
on conflict(fest_id, game_id, player_id) do update set
  source_team_id = excluded.source_team_id,
  override_team_id = excluded.override_team_id,
  updated_at = excluded.updated_at`,
			festID, override.GameID, playerID, sourceTeamID, overrideTeamID, now, now); err != nil {
			return nil, err
		}
		if gameType == "ek" {
			ekGames[override.GameID] = struct{}{}
		}
	}
	out := make([]int64, 0, len(ekGames))
	for gameID := range ekGames {
		if err := materializeGameRosterOverridesTx(ctx, tx, festID, gameID); err != nil {
			return nil, err
		}
		out = append(out, gameID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func festPlayerIDByRating(ctx context.Context, q store.Queryer, festID, ratingID int64) (int64, bool, error) {
	var id int64
	err := q.QueryRowContext(ctx, `
select id from fest_players
where fest_id = ? and rating_id = ?
order by id
limit 1`, festID, ratingID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

func activeFestTeamIDByRating(ctx context.Context, q store.Queryer, festID, ratingID int64) (int64, bool, error) {
	var id int64
	err := q.QueryRowContext(ctx, `
select id from fest_teams
where fest_id = ? and rating_id = ? and deleted = 0
order by id
limit 1`, festID, ratingID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}
