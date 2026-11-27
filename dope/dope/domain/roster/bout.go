package roster

import (
	"context"
	"database/sql"
	"strings"

	"dope/dope/domain/games"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
)

// A bout game that seats teams (EK, ES, brain, Hamsa) follows a source for
// who its teams are, never for who sits where (ADR-0038). Its entrant list,
// seeding and bouts change only by the host's hand; its teams' names, cities
// and players follow the source and its overrides. A plain one (the rating
// site, no override) reads its teams as it always did: the participants' own
// names and the rosters they brought, or the hand rosters of its roster tab.
// Any other keeps a name and city per team in game_team_names and the
// players as its hand rosters, written here after every change upstream. A
// player who has something entered in the game stays on the roster.

// Tethered says a game's teams follow a source or carry overrides.
func Tethered(ctx context.Context, q store.Queryer, gameID int64) (bool, error) {
	src, err := loadSource(ctx, q, gameID)
	return !src.plain() && !src.own, err
}

// BoutTeamGame reports whether a game seats teams in bouts: a hand-roster
// format that folds no roster.
func BoutTeamGame(gameType string) bool {
	_, flat := games.As[games.RosterFolder](gameType)
	return HandRoster(gameType) && !flat
}

// teamsGame reads a game whose teams follow a source: a flat game or a bout
// game that seats teams.
func teamsGame(ctx context.Context, q store.Queryer, festID, gameID int64) (store.GameDoc, error) {
	doc, err := store.LoadGameDoc(ctx, q, festID, gameID)
	if err != nil {
		return doc, err
	}
	if _, flat := games.As[games.RosterFolder](doc.GameType); !flat && !BoutTeamGame(doc.GameType) {
		return doc, corei18n.User(dopestrings.Default.Entrants.Own.NotFlat())
	}
	return doc, nil
}

// boutTeam is a team a bout game seats: its participant, the fest team key
// (empty for a one-off), and its name and city of its own. A participant is
// its fest team by link, else by number, which is a team's identity
// (ADR-0009).
type boutTeam struct {
	participant int64
	key         string
	number      int64
	name, city  string
}

func loadBoutTeams(ctx context.Context, q store.Queryer, doc store.GameDoc) ([]boutTeam, error) {
	ids, err := gameTeamIDs(ctx, q, doc.GameID, doc.State)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return store.CollectRows(ctx, q, `
select p.id, coalesce(p.number, 0), p.name, coalesce(p.city, ''), coalesce(t.rating_id, 0), coalesce(t.id, 0)
from participants p
left join fest_teams t on t.fest_id = p.fest_id and t.deleted = 0 and (t.id = p.fest_team_id
  or (p.fest_team_id is null and p.game_id is null and p.roster = 'team' and t.number = p.number))
where p.id in (`+placeholders(len(ids))+`) order by p.id`, args, func(rows *sql.Rows) (boutTeam, error) {
		var team boutTeam
		var ratingID, local int64
		if err := rows.Scan(&team.participant, &team.number, &team.name, &team.city, &ratingID, &local); err != nil {
			return team, err
		}
		if local > 0 {
			team.key = teamKey(FestRosterImportTeam{RatingID: ratingID, LocalID: local})
		}
		return team, nil
	})
}

// RefreshBoutGamesTx writes every tethered bout game's teams of the fest as
// their sources give them now, and answers the games it wrote.
func RefreshBoutGamesTx(ctx context.Context, tx *sql.Tx, festID int64) ([]int64, error) {
	docs, err := store.LoadGameDocs(ctx, tx, festID)
	if err != nil {
		return nil, err
	}
	var out []int64
	for _, doc := range docs {
		if !BoutTeamGame(doc.GameType) {
			continue
		}
		src, err := loadSource(ctx, tx, doc.GameID)
		if err != nil {
			return nil, err
		}
		if src.plain() {
			continue
		}
		if err := refreshBoutTx(ctx, tx, festID, doc, src); err != nil {
			return nil, err
		}
		out = append(out, doc.GameID)
	}
	return out, nil
}

// RefreshBoutGameTx writes one bout game's teams as its source gives them
// now, if it follows one; refreshed says it did.
func RefreshBoutGameTx(ctx context.Context, tx *sql.Tx, festID, gameID int64) (refreshed bool, err error) {
	doc, err := store.LoadGameDoc(ctx, tx, festID, gameID)
	if err != nil || !BoutTeamGame(doc.GameType) {
		return false, err
	}
	src, err := loadSource(ctx, tx, gameID)
	if err != nil || src.plain() {
		return false, err
	}
	return true, refreshBoutTx(ctx, tx, festID, doc, src)
}

// refreshBoutTx writes one bout game's teams: each seated fest team's name
// and city where they differ from its own, and its players as a hand roster
// with everybody who has something entered kept. A plain game drops both.
func refreshBoutTx(ctx context.Context, tx *sql.Tx, festID int64, doc store.GameDoc, src teamsSource) error {
	if src.plain() {
		if _, err := tx.ExecContext(ctx, `delete from game_team_names where game_id = ?`, doc.GameID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `delete from game_team_players where game_id = ? and hand = 1`, doc.GameID)
		return err
	}
	resolved, _, err := resolveTeams(ctx, tx, festID, src, map[int64]bool{})
	if err != nil {
		return err
	}
	teams, err := loadBoutTeams(ctx, tx, doc)
	if err != nil {
		return err
	}
	used, err := playersWithResults(ctx, tx, doc.GameID, doc.GameType)
	if err != nil {
		return err
	}
	for _, team := range teams {
		at := teamAt(resolved, team.key)
		if team.key == "" || at < 0 {
			continue
		}
		if err := writeBoutTeam(ctx, tx, festID, doc, team, resolved[at], used); err != nil {
			return err
		}
	}
	return touchGame(ctx, tx, doc.GameID)
}

func writeBoutTeam(ctx context.Context, tx *sql.Tx, festID int64, doc store.GameDoc, team boutTeam, resolved OwnTeam, used usedPlayers) error {
	if err := store.SetGameTeamName(ctx, tx, doc.GameID, team.participant, resolved.Name, resolved.City, team.name, team.city); err != nil {
		return err
	}
	game, err := loadRosterGame(ctx, tx, festID, doc.GameID)
	if err != nil {
		return err
	}
	before, err := currentRoster(ctx, tx, doc.GameID, game, team.participant)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(resolved.Players))
	seen := map[string]bool{}
	for _, p := range resolved.Players {
		names = append(names, p.Name)
		seen[util.AlphaKey(p.Name)] = true
	}
	for _, member := range before.Players {
		if used.has(team.participant, member) && !seen[util.AlphaKey(member.Name)] {
			names = append(names, member.Name)
		}
	}
	if _, err := tx.ExecContext(ctx, `delete from game_team_players where game_id = ? and participant_id = ?`, doc.GameID, team.participant); err != nil {
		return err
	}
	for order, name := range names {
		first, last := SplitPlayerName(name)
		playerID, err := EnsureSeedPlayer(ctx, tx, festID, first, last)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
insert or ignore into game_team_players(game_id, participant_id, player_id, roster_order, hand) values(?, ?, ?, ?, 1)`,
			doc.GameID, team.participant, playerID, order); err != nil {
			return err
		}
	}
	return nil
}

// handRostersToOverrides turns a plain bout game's hand rosters into player
// overrides on the teams its source gives it, so the game plays the same
// people once it follows the source. Its hand rosters go; the refresh writes
// them back from the overrides.
func handRostersToOverrides(ctx context.Context, tx *sql.Tx, festID int64, doc store.GameDoc, src teamsSource) (teamsSource, error) {
	base, _, err := resolveTeams(ctx, tx, festID, src, map[int64]bool{})
	if err != nil {
		return src, err
	}
	teams, err := loadBoutTeams(ctx, tx, doc)
	if err != nil {
		return src, err
	}
	game, err := loadRosterGame(ctx, tx, festID, doc.GameID)
	if err != nil {
		return src, err
	}
	for _, team := range teams {
		at := teamAt(base, team.key)
		if team.key == "" || at < 0 {
			continue
		}
		roster, err := currentRoster(ctx, tx, doc.GameID, game, team.participant)
		if err != nil {
			return src, err
		}
		if !roster.Hand {
			continue
		}
		src.overrides = append(src.overrides, rosterOverrides(base, at, roster.Players, nextOverrideID(src.overrides))...)
	}
	return src, nil
}

// rosterOverrides is the overrides that turn team at's players into hand:
// whoever is missing taken off, whoever is new added, or moved from the team
// the source has them on. Players are matched by name.
func rosterOverrides(base []OwnTeam, at int, hand []store.RosterMember, id int64) []Override {
	team := base[at]
	inHand := map[string]bool{}
	for _, member := range hand {
		inHand[util.AlphaKey(member.Name)] = true
	}
	var out []Override
	add := func(o Override) {
		o.ID, o.Label = id, team.Name
		id++
		out = append(out, o)
	}
	onTeam := map[string]bool{}
	for _, p := range team.Players {
		onTeam[util.AlphaKey(p.Name)] = true
		if !inHand[util.AlphaKey(p.Name)] {
			player := p
			add(Override{Kind: OverrideRemovePlayer, Team: team.Key, Player: &player})
		}
	}
	for _, member := range hand {
		if onTeam[util.AlphaKey(member.Name)] {
			continue
		}
		player, from := findPlayer(base, member.Name)
		if from >= 0 {
			add(Override{Kind: OverrideMovePlayer, To: team.Key, ToLabel: team.Name, Player: &player})
		} else {
			add(Override{Kind: OverrideAddPlayer, Team: team.Key, Player: &OwnPlayer{Name: strings.TrimSpace(member.Name)}})
		}
	}
	return out
}

// findPlayer is the player of that name on a team of the source, with the
// team's place, or -1.
func findPlayer(teams []OwnTeam, name string) (OwnPlayer, int) {
	key := util.AlphaKey(name)
	for i, team := range teams {
		for _, p := range team.Players {
			if util.AlphaKey(p.Name) == key {
				return p, i
			}
		}
	}
	return OwnPlayer{}, -1
}

// boutTeamsView is a bout game's teams on its tab: each seated fest team as
// its source and overrides give it.
func boutTeamsView(ctx context.Context, q store.Queryer, festID int64, doc store.GameDoc, src teamsSource) ([]OwnTeam, []Override, error) {
	resolved, overrides, err := resolveTeams(ctx, q, festID, src, map[int64]bool{})
	if err != nil {
		return nil, nil, err
	}
	teams, err := loadBoutTeams(ctx, q, doc)
	if err != nil {
		return nil, nil, err
	}
	out := make([]OwnTeam, 0, len(teams))
	for _, team := range teams {
		if at := teamAt(resolved, team.key); team.key != "" && at >= 0 {
			row := resolved[at]
			row.Number = team.number
			out = append(out, row)
		}
	}
	return out, overrides, nil
}
