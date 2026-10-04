package roster

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"dope/dope/domain/games"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// A game roster (CONTEXT.md, Game roster) is who a team plays one Game with.
// By default it is what the team brought: the fest roster, copied at the seed
// import, with the fest's player overrides for that Game applied. The host can
// keep it by hand instead, on the Game's roster tab, and then exactly those
// players play for the team in this Game: the fest roster, a seed import, a
// rating import and the overrides no longer reach it. The hand roster lives in
// game_team_players with hand = 1 (store.GameRosters reads it), and clearing
// the Game drops it together with the overrides.

// HandRoster reports whether the host edits a team's roster per Game in this
// format (games.Definition.HandRoster): the buzzer formats that seat teams.
// Troika seats troikas, whose people are edited on the troikas page; personal
// SI seats players, not teams.
func HandRoster(gameType string) bool {
	d, ok := games.Lookup(gameType)
	return ok && d.HandRoster
}

// GameRosterPlayer is one player on a team's game roster. Locked says the
// player already has something entered in this Game, so the host cannot take
// them off the roster.
type GameRosterPlayer struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	RatingID int64  `json:"ratingID,omitempty"`
	Locked   bool   `json:"locked,omitempty"`
}

// GameRosterTeam is one team of a Game with the roster it plays this Game
// with, and whether the host kept that roster by hand.
type GameRosterTeam struct {
	ParticipantID int64              `json:"participantID"`
	Number        int64              `json:"number,omitempty"`
	Name          string             `json:"name"`
	City          string             `json:"city,omitempty"`
	Players       []GameRosterPlayer `json:"players"`
	Hand          bool               `json:"hand,omitempty"`
}

// rosterGame is what the roster code needs to know about a Game.
type rosterGame struct {
	gameType     string
	rosterSource string
	state        string
}

func loadRosterGame(ctx context.Context, q store.Queryer, festID, gameID int64) (rosterGame, error) {
	var g rosterGame
	err := q.QueryRowContext(ctx, `
select game_type, coalesce(roster_source, 'fest'), coalesce(state_json, '{}') from games where id = ? and fest_id = ?`,
		gameID, festID).Scan(&g.gameType, &g.rosterSource, &g.state)
	return g, err
}

// gameTeamIDs lists the teams a Game has, in the order the Game gives them:
// the entrants it named, then its seed list, then whoever sits in its bouts.
func gameTeamIDs(ctx context.Context, q store.Queryer, gameID int64, state string) ([]int64, error) {
	var out []int64
	seen := map[int64]bool{}
	add := func(id int64) {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	named, err := store.CollectRows(ctx, q, `
select participant_id from game_participants where game_id = ? order by position, participant_id`, []any{gameID}, scanID)
	if err != nil {
		return nil, err
	}
	for _, id := range named {
		add(id)
	}
	var doc struct {
		SeedImport struct {
			Rows []struct {
				TeamID int64 `json:"teamID"`
			} `json:"rows"`
		} `json:"seedImport"`
	}
	_ = json.Unmarshal([]byte(state), &doc)
	for _, row := range doc.SeedImport.Rows {
		add(row.TeamID)
	}
	seated, err := store.CollectRows(ctx, q, `
select ms.participant_id from match_slots ms join matches m on m.id = ms.match_id
where m.game_id = ? and ms.participant_id is not null
order by m.position, m.id, ms.slot_index`, []any{gameID}, scanID)
	if err != nil {
		return nil, err
	}
	for _, id := range seated {
		add(id)
	}
	return out, nil
}

func scanID(rows *sql.Rows) (int64, error) {
	var id int64
	return id, rows.Scan(&id)
}

// LoadGameRosters lists a Game's teams with the rosters they play it with. It
// is empty while the Game has no teams yet (before its seed is imported).
func LoadGameRosters(ctx context.Context, q store.Queryer, festID, gameID int64) ([]GameRosterTeam, error) {
	game, err := loadRosterGame(ctx, q, festID, gameID)
	if err != nil {
		return nil, err
	}
	ids, err := gameTeamIDs(ctx, q, gameID, game.state)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	rosters, err := store.GameRosters(ctx, q, gameID, game.rosterSource, ids)
	if err != nil {
		return nil, err
	}
	used, err := playersWithResults(ctx, q, gameID, game.gameType)
	if err != nil {
		return nil, err
	}
	ratings, err := festPlayerRatings(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	type head struct {
		number     int64
		name, city string
	}
	heads := map[int64]head{}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := store.CollectRows(ctx, q, `
select id, coalesce(number, 0), name, coalesce(city, '') from participants where id in (`+placeholders(len(ids))+`)`, args,
		func(rows *sql.Rows) (struct {
			id int64
			h  head
		}, error) {
			var r struct {
				id int64
				h  head
			}
			return r, rows.Scan(&r.id, &r.h.number, &r.h.name, &r.h.city)
		})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		heads[r.id] = r.h
	}
	out := make([]GameRosterTeam, 0, len(ids))
	for _, id := range ids {
		h := heads[id]
		roster := rosters[id]
		team := GameRosterTeam{ParticipantID: id, Number: h.number, Name: h.name, City: h.city, Hand: roster.Hand, Players: []GameRosterPlayer{}}
		for _, member := range roster.Players {
			team.Players = append(team.Players, GameRosterPlayer{
				ID:       member.ID,
				Name:     member.Name,
				RatingID: ratings[util.AlphaKey(member.Name)],
				Locked:   used.has(id, member),
			})
		}
		out = append(out, team)
	}
	return out, nil
}

// festPlayerRatings maps a fest player's name to their rating.chgk.info id, so
// the game roster links a player the way the fest roster does.
func festPlayerRatings(ctx context.Context, q store.Queryer, festID int64) (map[string]int64, error) {
	rows, err := store.CollectRows(ctx, q, `
select first_name, last_name, rating_id from fest_players where fest_id = ? and coalesce(rating_id, 0) > 0`, []any{festID},
		func(rows *sql.Rows) (struct {
			first, last string
			rating      int64
		}, error) {
			var r struct {
				first, last string
				rating      int64
			}
			return r, rows.Scan(&r.first, &r.last, &r.rating)
		})
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[util.AlphaKey(store.JoinPlayerName(r.first, r.last))] = r.rating
	}
	return out, nil
}

// usedPlayers is who already has something entered in a Game, per team: by
// player id where the format records ids (EK, ES, Hamsa), by name where it
// records the name (brain).
type usedPlayers struct {
	ids   map[int64]map[int64]bool
	names map[int64]map[string]bool
}

func (u usedPlayers) has(team int64, member store.RosterMember) bool {
	return u.ids[team][member.ID] || u.names[team][util.AlphaKey(member.Name)]
}

func (u usedPlayers) markID(team, player int64) {
	if player == 0 {
		return
	}
	if u.ids[team] == nil {
		u.ids[team] = map[int64]bool{}
	}
	u.ids[team][player] = true
}

// playersWithResults reads every bout of the Game for the players it names,
// through the format's Protocol (games.PlayersUser).
func playersWithResults(ctx context.Context, q store.Queryer, gameID int64, gameType string) (usedPlayers, error) {
	used := usedPlayers{ids: map[int64]map[int64]bool{}, names: map[int64]map[string]bool{}}
	type match struct {
		id    int64
		state string
	}
	matches, err := store.CollectRows(ctx, q, `
select id, coalesce(state_json, '{}') from matches where game_id = ?`, []any{gameID}, func(rows *sql.Rows) (match, error) {
		var m match
		return m, rows.Scan(&m.id, &m.state)
	})
	if err != nil {
		return used, err
	}
	type slot struct{ match, index, participant int64 }
	slots, err := store.CollectRows(ctx, q, `
select ms.match_id, ms.slot_index, coalesce(ms.participant_id, 0) from match_slots ms
join matches m on m.id = ms.match_id where m.game_id = ?`, []any{gameID}, func(rows *sql.Rows) (slot, error) {
		var s slot
		return s, rows.Scan(&s.match, &s.index, &s.participant)
	})
	if err != nil {
		return used, err
	}
	seats := map[int64][]int64{}
	for _, s := range slots {
		if s.index < 0 {
			continue
		}
		row := seats[s.match]
		for int64(len(row)) <= s.index {
			row = append(row, 0)
		}
		row[s.index] = s.participant
		seats[s.match] = row
	}
	user, ok := games.As[games.PlayersUser](gameType)
	if !ok {
		return used, nil
	}
	for _, m := range matches {
		for _, p := range user.UsedPlayers(json.RawMessage(m.state), seats[m.id]) {
			used.markID(p.Team, p.Player)
			if name := strings.TrimSpace(p.Name); name != "" {
				if used.names[p.Team] == nil {
					used.names[p.Team] = map[string]bool{}
				}
				used.names[p.Team][util.AlphaKey(name)] = true
			}
		}
	}
	return used, nil
}

// editableTeam checks that the Game is one whose rosters the host edits and
// that the team is one of its own, and returns the Game.
func editableTeam(ctx context.Context, q store.Queryer, festID, gameID, participantID int64) (rosterGame, error) {
	s := dopestrings.Default
	game, err := loadRosterGame(ctx, q, festID, gameID)
	if err != nil {
		return game, err
	}
	if !HandRoster(game.gameType) {
		return game, corei18n.User(s.Fest.RosterEdit.WrongFormat())
	}
	ids, err := gameTeamIDs(ctx, q, gameID, game.state)
	if err != nil {
		return game, err
	}
	if !slices.Contains(ids, participantID) {
		return game, corei18n.User(s.Fest.RosterEdit.TeamNotInGame())
	}
	return game, nil
}

// currentRoster is the team's roster in this Game right now.
func currentRoster(ctx context.Context, q store.Queryer, gameID int64, game rosterGame, participantID int64) (store.GameRoster, error) {
	rosters, err := store.GameRosters(ctx, q, gameID, game.rosterSource, []int64{participantID})
	if err != nil {
		return store.GameRoster{}, err
	}
	return rosters[participantID], nil
}

// refuseDroppedWithResults refuses a roster change that takes off a player who
// already has something entered for the team in this Game.
func refuseDroppedWithResults(ctx context.Context, q store.Queryer, gameID int64, game rosterGame, participantID int64, before []store.RosterMember, after map[string]bool) error {
	used, err := playersWithResults(ctx, q, gameID, game.gameType)
	if err != nil {
		return err
	}
	for _, member := range before {
		if !after[util.AlphaKey(member.Name)] && used.has(participantID, member) {
			return corei18n.User(dopestrings.Default.Fest.RosterEdit.PlayerHasResults(member.Name))
		}
	}
	return nil
}

// SaveGameRosterTx keeps a team's roster in this Game by hand: exactly the
// people typed, in that order. A person is typed as the rating roster writes
// them, first name then surname, and whatever follows an opening bracket (the
// suggestion's team) is dropped. A new name is a new person of the fest. The
// roster may not be empty, may not name anybody twice, and may not drop a
// player who already has something entered for the team in this Game.
func SaveGameRosterTx(ctx context.Context, tx *sql.Tx, festID, gameID, participantID int64, typed []string) error {
	s := dopestrings.Default
	game, err := editableTeam(ctx, tx, festID, gameID, participantID)
	if err != nil {
		return err
	}
	var people [][2]string
	after := map[string]bool{}
	for _, entry := range typed {
		first, last := SplitPlayerName(entry)
		if first == "" && last == "" {
			continue
		}
		key := util.AlphaKey(store.JoinPlayerName(first, last))
		if after[key] {
			return corei18n.User(s.Fest.RosterEdit.PlayerTwice(store.JoinPlayerName(first, last)))
		}
		after[key] = true
		people = append(people, [2]string{first, last})
	}
	if len(people) == 0 {
		return corei18n.User(s.Fest.RosterEdit.Empty())
	}
	before, err := currentRoster(ctx, tx, gameID, game, participantID)
	if err != nil {
		return err
	}
	if err := refuseDroppedWithResults(ctx, tx, gameID, game, participantID, before.Players, after); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from game_team_players where game_id = ? and participant_id = ?`, gameID, participantID); err != nil {
		return err
	}
	for order, person := range people {
		playerID, err := EnsureSeedPlayer(ctx, tx, festID, person[0], person[1])
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
insert into game_team_players(game_id, participant_id, player_id, roster_order, hand) values(?, ?, ?, ?, 1)`,
			gameID, participantID, playerID, order); err != nil {
			return err
		}
	}
	return touchGame(ctx, tx, gameID)
}

// ResetGameRosterTx gives a team back the roster it brought: the host's hand
// roster goes, and the fest roster with the Game's overrides applies again. It
// is refused when that would drop a player who has something entered.
func ResetGameRosterTx(ctx context.Context, tx *sql.Tx, festID, gameID, participantID int64, materialize func(ctx context.Context, tx *sql.Tx) error) error {
	game, err := editableTeam(ctx, tx, festID, gameID, participantID)
	if err != nil {
		return err
	}
	before, err := currentRoster(ctx, tx, gameID, game, participantID)
	if err != nil {
		return err
	}
	if !before.Hand {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `delete from game_team_players where game_id = ? and participant_id = ? and hand = 1`, gameID, participantID); err != nil {
		return err
	}
	if game.rosterSource == "game" && materialize != nil {
		if err := materialize(ctx, tx); err != nil {
			return err
		}
	}
	after, err := currentRoster(ctx, tx, gameID, game, participantID)
	if err != nil {
		return err
	}
	kept := map[string]bool{}
	for _, member := range after.Players {
		kept[util.AlphaKey(member.Name)] = true
	}
	if err := refuseDroppedWithResults(ctx, tx, gameID, game, participantID, before.Players, kept); err != nil {
		return err
	}
	return touchGame(ctx, tx, gameID)
}

func touchGame(ctx context.Context, tx *sql.Tx, gameID int64) error {
	_, err := tx.ExecContext(ctx, `update games set updated_at = ? where id = ?`, util.UtcNow(), gameID)
	return err
}

// HandRosterTeams lists the teams of a Game whose roster the host keeps by
// hand, for the overrides that cannot reach them.
func HandRosterTeams(ctx context.Context, q store.Queryer, gameID int64) (map[int64]bool, error) {
	ids, err := store.CollectRows(ctx, q, `
select distinct participant_id from game_team_players where game_id = ? and hand = 1`, []any{gameID}, scanID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
