package roster

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"dope/dope/domain/games"
	"dope/dope/domain/multifest"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
)

// A flat Game's entrants tab (OD, the minefield, KSI, Multi; ADR-0038): its
// source (the rating site, or another Game) with the overrides it
// adds, or its separate list, edited freely in the fest's team editor. Going
// to a separate list copies the teams as they stand; going back drops it.

// OwnTeam is a team as a flat Game has it: on its separate list, or worked
// out from its source. Key names it across syncs. On a save, Prev is the
// number the row had before, which carries the team's answers to its new
// one. Entered says the Game holds answers for the team, Overridden which
// overrides changed it, and Missing that it left the source but stays for
// its answers. Only the list's teams are stored, and without those four.
type OwnTeam struct {
	Key        string      `json:"key,omitempty"`
	Number     int64       `json:"number"`
	Name       string      `json:"name"`
	City       string      `json:"city,omitempty"`
	RatingID   int64       `json:"ratingID,omitempty"`
	Flags      []string    `json:"flags,omitempty"`
	Players    []OwnPlayer `json:"players,omitempty"`
	Prev       int64       `json:"prev,omitempty"`
	Entered    bool        `json:"entered,omitempty"`
	Overridden []string    `json:"overridden,omitempty"`
	Missing    bool        `json:"missing,omitempty"`
}

func (t OwnTeam) stored() OwnTeam {
	t.Prev, t.Entered, t.Overridden, t.Missing = 0, false, nil, false
	return t
}

// OwnPlayer is one person on a team.
type OwnPlayer struct {
	Name     string `json:"name"`
	RatingID int64  `json:"ratingID,omitempty"`
}

// SourceGame is a Game another one may follow.
type SourceGame struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// GameTeamsView is a flat Game's entrants tab: its source (From, 0 for the
// rating site) or its separate list (Own), the Games it may follow, its
// teams, its overrides, and why it cannot take its source's last change.
type GameTeamsView struct {
	Own       bool         `json:"own"`
	From      int64        `json:"from"`
	Sources   []SourceGame `json:"sources"`
	Teams     []OwnTeam    `json:"teams"`
	Overrides []Override   `json:"overrides"`
	Conflict  string       `json:"conflict,omitempty"`
	// Bout marks a bout game, which follows a source for who its teams are
	// and seats them by its entrant list.
	Bout bool `json:"bout,omitempty"`
}

// festSeated is the fest's teams a Game on venueID seats, under the venue's
// numbers, each with its players.
func festSeated(ctx context.Context, q store.Queryer, festID, venueID int64) ([]FestRosterImportTeam, error) {
	teams, err := LoadFestRosterImportTeamsTx(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	players, err := festTeamPlayers(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	for i := range teams {
		teams[i].Players = players[teams[i].LocalID]
	}
	placement, err := multifest.LoadPlacement(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	return OnVenue(teams, placement, venueID), nil
}

// festTeamPlayers is each fest team's players, in roster order, by team row.
func festTeamPlayers(ctx context.Context, q store.Queryer, festID int64) (map[int64][]FestRosterImportPlayer, error) {
	type row struct {
		team   int64
		player FestRosterImportPlayer
	}
	rows, err := store.CollectRows(ctx, q, `
select ftp.team_id, coalesce(p.rating_id, 0), p.first_name, p.last_name
from fest_team_players ftp join fest_players p on p.id = ftp.player_id
join fest_teams t on t.id = ftp.team_id
where t.fest_id = ? and t.deleted = 0
order by ftp.team_id, ftp.roster_order`, []any{festID}, func(rows *sql.Rows) (row, error) {
		var r row
		return r, rows.Scan(&r.team, &r.player.RatingID, &r.player.FirstName, &r.player.LastName)
	})
	if err != nil {
		return nil, err
	}
	out := map[int64][]FestRosterImportPlayer{}
	for _, r := range rows {
		out[r.team] = append(out[r.team], r.player)
	}
	return out, nil
}

// GameSeatsTeamsTx is the teams a flat Game seats now, as its source and
// overrides give them: what creation and Clear fold in.
func GameSeatsTeamsTx(ctx context.Context, q store.Queryer, festID, gameID, venueID int64) ([]FestRosterImportTeam, error) {
	src, err := loadSource(ctx, q, gameID)
	if err != nil {
		return nil, err
	}
	if src.plain() {
		return festSeated(ctx, q, festID, venueID)
	}
	teams, _, err := resolveTeams(ctx, q, festID, src, map[int64]bool{})
	return importTeams(teams), err
}

// importTeams is a Game's teams as the roster code carries teams.
func importTeams(list []OwnTeam) []FestRosterImportTeam {
	out := make([]FestRosterImportTeam, 0, len(list))
	for _, team := range list {
		row := FestRosterImportTeam{Number: team.Number, Name: team.Name, City: team.City, RatingID: team.RatingID}
		for _, flag := range team.Flags {
			row.Flags = append(row.Flags, FestRosterFlag{Short: flag})
		}
		for _, player := range team.Players {
			first, last, _ := strings.Cut(strings.TrimSpace(player.Name), " ")
			row.Players = append(row.Players, FestRosterImportPlayer{RatingID: player.RatingID, FirstName: first, LastName: strings.TrimSpace(last)})
		}
		out = append(out, row)
	}
	return out
}

// OwnTeams is the roster code's teams as a Game's, each with its key.
func OwnTeams(teams []FestRosterImportTeam) []OwnTeam {
	out := make([]OwnTeam, 0, len(teams))
	for _, team := range teams {
		row := OwnTeam{Key: teamKey(team), Number: team.Number, Name: team.Name, City: team.City, RatingID: team.RatingID, Flags: FlagShortNames(team.Flags)}
		for _, player := range team.Players {
			row.Players = append(row.Players, OwnPlayer{Name: importPlayerName(player), RatingID: player.RatingID})
		}
		out = append(out, row)
	}
	return out
}

// NumberedOwnTeams gives each team without a number the next one free.
func NumberedOwnTeams(teams []OwnTeam) []OwnTeam {
	top := topNumber(teams)
	for i := range teams {
		if teams[i].Number <= 0 {
			top++
			teams[i].Number = top
		}
	}
	return teams
}

func topNumber(teams []OwnTeam) int64 {
	var top int64
	for _, team := range teams {
		top = max(top, team.Number)
	}
	return top
}

// LoadGameTeams reads a flat Game's entrants tab.
func LoadGameTeams(ctx context.Context, q store.Queryer, festID, gameID int64) (GameTeamsView, error) {
	doc, err := teamsGame(ctx, q, festID, gameID)
	if err != nil {
		return GameTeamsView{}, err
	}
	src, err := loadSource(ctx, q, gameID)
	if err != nil {
		return GameTeamsView{}, err
	}
	sources, err := sourceGames(ctx, q, festID, doc)
	if err != nil {
		return GameTeamsView{}, err
	}
	if BoutTeamGame(doc.GameType) {
		teams, overrides, err := boutTeamsView(ctx, q, festID, doc, src)
		if overrides == nil {
			overrides = []Override{}
		}
		return GameTeamsView{From: src.from, Sources: sources, Teams: teams, Overrides: overrides, Bout: true}, err
	}
	plan, overrides, err := gamePlan(ctx, q, festID, doc)
	if err != nil {
		return GameTeamsView{}, err
	}
	entered := map[int64]bool{}
	for _, seat := range enteredSeats(doc) {
		entered[seat.Number] = true
	}
	view := GameTeamsView{Own: src.own, From: src.from, Sources: sources, Teams: plan.teams, Overrides: overrides, Conflict: plan.conflict}
	if view.Overrides == nil {
		view.Overrides = []Override{}
	}
	for i := range view.Teams {
		view.Teams[i].Entered = entered[view.Teams[i].Number]
	}
	return view, nil
}

// sourceGames is the Games a Game may follow: the fest's other flat Games on
// its venue that do not follow it.
func sourceGames(ctx context.Context, q store.Queryer, festID int64, doc store.GameDoc) ([]SourceGame, error) {
	docs, err := store.LoadGameDocs(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	out := []SourceGame{}
	for _, other := range docs {
		if other.GameID == doc.GameID || other.MultiFestVenueID != doc.MultiFestVenueID {
			continue
		}
		if _, ok := games.As[games.RosterFolder](other.GameType); !ok {
			continue
		}
		loops, err := follows(ctx, q, other.GameID, doc.GameID)
		if err != nil {
			return nil, err
		}
		if loops {
			continue
		}
		game := SourceGame{ID: other.GameID}
		if err := q.QueryRowContext(ctx, `select title from games where id = ?`, other.GameID).Scan(&game.Title); err != nil {
			return nil, err
		}
		out = append(out, game)
	}
	return out, nil
}

// follows says whether game takes its teams from target, at any distance.
func follows(ctx context.Context, q store.Queryer, game, target int64) (bool, error) {
	seen := map[int64]bool{}
	for game > 0 && !seen[game] {
		if game == target {
			return true, nil
		}
		seen[game] = true
		src, err := loadSource(ctx, q, game)
		if err != nil || src.own {
			return false, err
		}
		game = src.from
	}
	return false, nil
}

// GameExportTeams is the teams a game that follows a source or carries
// overrides plays with, by key, for the export to the rating site; nil for a
// plain game, whose teams are the fest's.
func GameExportTeams(ctx context.Context, q store.Queryer, festID, gameID int64) (map[string]OwnTeam, error) {
	src, err := loadSource(ctx, q, gameID)
	if err != nil || src.plain() {
		return nil, err
	}
	resolved, _, err := resolveTeams(ctx, q, festID, src, map[int64]bool{})
	if err != nil {
		return nil, err
	}
	out := make(map[string]OwnTeam, len(resolved))
	for _, team := range resolved {
		out[team.Key] = team
	}
	return out, nil
}

// FestTeamKey is a fest team's key across syncs: its rating site id, else
// its row.
func FestTeamKey(ratingID, teamID int64) string {
	return teamKey(FestRosterImportTeam{RatingID: ratingID, LocalID: teamID})
}

// GameSheetTeams is the teams a flat Game's entrants tab offers as a roster
// sheet: the teams it seats.
func GameSheetTeams(ctx context.Context, q store.Queryer, festID, gameID int64) ([]FestRosterImportTeam, error) {
	doc, err := flatGame(ctx, q, festID, gameID)
	if err != nil {
		return nil, err
	}
	return GameSeatsTeamsTx(ctx, q, festID, gameID, doc.MultiFestVenueID)
}

// TetheredRosterView is the roster tab of a flat Game that is not plain: its
// teams with their people. ok is false for a plain Game, whose tab is the
// fest roster's.
func TetheredRosterView(ctx context.Context, q store.Queryer, festID, gameID int64) (teams []FestRosterTeamView, ok bool, err error) {
	src, err := loadSource(ctx, q, gameID)
	if err != nil || src.plain() {
		return nil, false, err
	}
	// A bout game's tab lists the teams it seats, with the players written
	// for them (refreshBoutTx).
	var gameType string
	if err := q.QueryRowContext(ctx, `select game_type from games where id = ?`, gameID).Scan(&gameType); err != nil || BoutTeamGame(gameType) {
		return nil, false, err
	}
	resolved, _, err := resolveTeams(ctx, q, festID, src, map[int64]bool{})
	if err != nil {
		return nil, false, err
	}
	teams = make([]FestRosterTeamView, 0, len(resolved))
	for _, team := range resolved {
		view := FestRosterTeamView{Number: team.Number, Name: team.Name, City: team.City, RatingID: team.RatingID, Flags: team.Flags, Players: []FestRosterPlayerView{}}
		for _, player := range team.Players {
			view.Players = append(view.Players, FestRosterPlayerView{Name: player.Name, RatingID: player.RatingID})
		}
		teams = append(teams, view)
	}
	return teams, true, nil
}

// SetTeamsSourceTx has a flat Game play a separate list (own), made from the
// teams it seats now, or follow a source: the fest's teams (from 0) or
// another Game. Following a source drops the list.
func SetTeamsSourceTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, own bool, from int64) ([]GameStateBroadcast, error) {
	doc, src, err := changingGame(ctx, tx, festID, gameID)
	if err != nil {
		return nil, err
	}
	if BoutTeamGame(doc.GameType) {
		if own {
			return nil, corei18n.User(dopestrings.Default.Entrants.Own.BoutNoList())
		}
		if src.plain() && from > 0 {
			if src, err = handRostersToOverrides(ctx, tx, festID, doc, src); err != nil {
				return nil, err
			}
		}
	}
	if own {
		teams, _, err := resolveTeams(ctx, tx, festID, src, map[int64]bool{})
		if err != nil {
			return nil, err
		}
		src.list, src.overrides = NumberedOwnTeams(teams), nil
	} else {
		if err := checkSourceGame(ctx, tx, festID, doc, from); err != nil {
			return nil, err
		}
		src.list, src.from = nil, from
	}
	src.own = own
	if err := storeSource(ctx, tx, src); err != nil {
		return nil, err
	}
	return refoldWithFollowersTx(ctx, tx, festID, doc)
}

// checkSourceGame refuses a source Game that is not a flat Game of the
// fest on the same venue, or that follows the Game itself.
func checkSourceGame(ctx context.Context, q store.Queryer, festID int64, doc store.GameDoc, from int64) error {
	if from == 0 {
		return nil
	}
	source, err := flatGame(ctx, q, festID, from)
	if err != nil {
		return err
	}
	if source.MultiFestVenueID != doc.MultiFestVenueID {
		return corei18n.User(dopestrings.Default.Entrants.Own.NotFlat())
	}
	if loops, err := follows(ctx, q, from, doc.GameID); err != nil || loops {
		if err == nil {
			err = corei18n.User(dopestrings.Default.Entrants.Own.Cycle())
		}
		return err
	}
	return nil
}

// changingGame reads a flat Game about to change its teams, and records its
// numbers as they stand, which the fold after the change moves answers by.
func changingGame(ctx context.Context, tx *sql.Tx, festID, gameID int64) (store.GameDoc, teamsSource, error) {
	doc, err := teamsGame(ctx, tx, festID, gameID)
	if err != nil {
		return doc, teamsSource{}, err
	}
	if err := keepNumbers(ctx, tx, festID, doc); err != nil {
		return doc, teamsSource{}, err
	}
	src, err := loadSource(ctx, tx, gameID)
	return doc, src, err
}

// SaveOwnTeamsTx replaces a flat Game's separate list. Every team needs a
// name and a number of its own, and a team the Game holds answers for stays
// (its Prev names it), its answers following it to a new number.
func SaveOwnTeamsTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, teams []OwnTeam) ([]GameStateBroadcast, error) {
	doc, src, err := changingGame(ctx, tx, festID, gameID)
	if err != nil {
		return nil, err
	}
	if !src.own {
		return nil, corei18n.User(dopestrings.Default.Entrants.Own.Follows())
	}
	if teams, err = checkOwnTeams(teams, doc); err != nil {
		return nil, err
	}
	// The fold moves answers by key: a team's key keeps the number it had.
	numbers := map[string]int64{}
	for _, team := range teams {
		if team.Prev > 0 {
			numbers[team.Key] = team.Prev
		}
	}
	if err := storeNumbers(ctx, tx, gameID, numbers); err != nil {
		return nil, err
	}
	src.list = teams
	if err := storeSource(ctx, tx, src); err != nil {
		return nil, err
	}
	return refoldWithFollowersTx(ctx, tx, festID, doc)
}

// checkOwnTeams tidies a separate list a host sent: each team a key of its
// own, Divisions as they are typed. It refuses one without a name or a
// number, with a number twice, or without a team the Game holds answers for.
func checkOwnTeams(teams []OwnTeam, doc store.GameDoc) ([]OwnTeam, error) {
	out := make([]OwnTeam, 0, len(teams))
	byNumber := map[int64]string{}
	keys := map[string]bool{}
	kept := map[int64]bool{}
	for i, team := range teams {
		team.Name, team.City = strings.TrimSpace(team.Name), strings.TrimSpace(team.City)
		team.Flags = FlagShortNames(ParseFlags(strings.Join(team.Flags, ",")))
		switch {
		case team.Name == "":
			return nil, corei18n.User(dopestrings.Default.Entrants.Own.NameMissing(fmt.Sprint(i + 1)))
		case team.Number <= 0:
			return nil, corei18n.User(dopestrings.Default.Entrants.Own.NumberMissing(team.Name))
		case byNumber[team.Number] != "":
			return nil, corei18n.User(dopestrings.Default.Entrants.Own.NumberTaken(fmt.Sprint(team.Number), byNumber[team.Number], team.Name))
		}
		for team.Key == "" || keys[team.Key] {
			team.Key = fmt.Sprintf("o%d-%d", team.Number, len(keys))
		}
		keys[team.Key] = true
		byNumber[team.Number] = team.Name
		kept[team.Prev] = true
		out = append(out, team)
	}
	for _, seat := range enteredSeats(doc) {
		if !kept[seat.Number] {
			return nil, corei18n.User(dopestrings.Default.Entrants.Own.Entered(seat.Name))
		}
	}
	return out, nil
}

// flatGame reads a Game whose format folds the roster in.
func flatGame(ctx context.Context, q store.Queryer, festID, gameID int64) (store.GameDoc, error) {
	doc, err := store.LoadGameDoc(ctx, q, festID, gameID)
	if err != nil {
		return doc, err
	}
	if _, ok := games.As[games.RosterFolder](doc.GameType); !ok {
		return doc, corei18n.User(dopestrings.Default.Entrants.Own.NotFlat())
	}
	return doc, nil
}
