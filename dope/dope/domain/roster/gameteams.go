package roster

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"dope/dope/domain/games"
	"dope/dope/domain/multifest"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
)

// A flat Game's teams (OD, the minefield, KSI, Multi): by default the
// fest's whole roster, its venue's part for a Game on one, kept in step with
// every rating import and every hand edit on the fest teams page
// (PropagateRosterTx). The host may switch the Game to a separate list on its
// entrants tab. The list starts as a copy of the teams the Game seats at that
// moment and from then on is the Game's alone: its names, cities, numbers,
// Divisions and people change only there, by hand or from a roster sheet.
// The Game keeps the list when it goes back to the fest's teams, so the host
// may switch back and forth.

// OwnTeam is a team on a flat Game's separate list. On a save, Prev is the
// number the row had before, which carries the team's answers to its new
// one; Entered says the Game holds something a host entered for the team.
// Neither is stored.
type OwnTeam struct {
	Number   int64       `json:"number"`
	Name     string      `json:"name"`
	City     string      `json:"city,omitempty"`
	RatingID int64       `json:"ratingID,omitempty"`
	Flags    []string    `json:"flags,omitempty"`
	Players  []OwnPlayer `json:"players,omitempty"`
	Prev     int64       `json:"prev,omitempty"`
	Entered  bool        `json:"entered,omitempty"`
}

// OwnPlayer is one person on a separate list's team.
type OwnPlayer struct {
	Name     string `json:"name"`
	RatingID int64  `json:"ratingID,omitempty"`
}

// GameTeamsView is a flat Game's entrants tab: whether it plays its separate
// list, and the teams it seats (the list, or the fest's teams).
type GameTeamsView struct {
	Own   bool      `json:"own"`
	Teams []OwnTeam `json:"teams"`
}

// ownList is a Game's separate list as stored, and whether the Game plays it.
type ownList struct {
	own   bool
	teams []OwnTeam
}

// loadOwnList reads the Game's separate list; none for a Game not made yet
// (gameID 0).
func loadOwnList(ctx context.Context, q store.Queryer, gameID int64) (ownList, error) {
	if gameID == 0 {
		return ownList{}, nil
	}
	var list ownList
	var raw string
	if err := q.QueryRowContext(ctx, `select own_teams, own_roster from games where id = ?`, gameID).Scan(&list.own, &raw); err != nil {
		return list, err
	}
	if raw == "" {
		return list, nil
	}
	return list, json.Unmarshal([]byte(raw), &list.teams)
}

// storeOwnList writes the Game's separate list and whether it plays it.
func storeOwnList(ctx context.Context, tx *sql.Tx, gameID int64, list ownList) error {
	stored := make([]OwnTeam, len(list.teams))
	for i, team := range list.teams {
		team.Prev, team.Entered = 0, false
		stored[i] = team
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `update games set own_teams = ?, own_roster = ? where id = ?`, list.own, string(raw), gameID)
	return err
}

// festSeated is the fest's teams a Game on venueID seats, under the venue's
// numbers.
func festSeated(ctx context.Context, q store.Queryer, festID, venueID int64) ([]FestRosterImportTeam, error) {
	teams, err := LoadFestRosterImportTeamsTx(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	placement, err := multifest.LoadPlacement(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	return OnVenue(teams, placement, venueID), nil
}

// GameSeatsTeamsTx is the teams a flat Game seats now: its separate list, or
// the fest's roster on the Game's venue under the venue's numbers.
func GameSeatsTeamsTx(ctx context.Context, q store.Queryer, festID, gameID, venueID int64) ([]FestRosterImportTeam, error) {
	list, err := loadOwnList(ctx, q, gameID)
	if err != nil {
		return nil, err
	}
	if list.own {
		return importTeams(list.teams), nil
	}
	return festSeated(ctx, q, festID, venueID)
}

// importTeams is a separate list as the roster code carries teams.
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

// OwnTeams is the roster code's teams as a separate list's rows.
func OwnTeams(teams []FestRosterImportTeam) []OwnTeam {
	out := make([]OwnTeam, 0, len(teams))
	for _, team := range teams {
		row := OwnTeam{Number: team.Number, Name: team.Name, City: team.City, RatingID: team.RatingID, Flags: FlagShortNames(team.Flags)}
		for _, player := range team.Players {
			row.Players = append(row.Players, OwnPlayer{Name: importPlayerName(player), RatingID: player.RatingID})
		}
		out = append(out, row)
	}
	return out
}

// NumberedOwnTeams gives each team without a number the next one free.
func NumberedOwnTeams(teams []OwnTeam) []OwnTeam {
	var top int64
	for _, team := range teams {
		top = max(top, team.Number)
	}
	for i := range teams {
		if teams[i].Number <= 0 {
			top++
			teams[i].Number = top
		}
	}
	return teams
}

// LoadGameTeams reads a flat Game's entrants tab.
func LoadGameTeams(ctx context.Context, q store.Queryer, festID, gameID int64) (GameTeamsView, error) {
	doc, err := flatGame(ctx, q, festID, gameID)
	if err != nil {
		return GameTeamsView{}, err
	}
	teams, err := GameSeatsTeamsTx(ctx, q, festID, gameID, doc.MultiFestVenueID)
	if err != nil {
		return GameTeamsView{}, err
	}
	list, err := loadOwnList(ctx, q, gameID)
	if err != nil {
		return GameTeamsView{}, err
	}
	entered := enteredNumbers(doc)
	view := GameTeamsView{Own: list.own, Teams: OwnTeams(teams)}
	for i := range view.Teams {
		view.Teams[i].Entered = entered[view.Teams[i].Number]
	}
	return view, nil
}

// GameSheetTeams is the teams a flat Game's entrants tab offers as a roster
// sheet: its separate list, or the fest's teams it seats.
func GameSheetTeams(ctx context.Context, q store.Queryer, festID, gameID int64) ([]FestRosterImportTeam, error) {
	doc, err := flatGame(ctx, q, festID, gameID)
	if err != nil {
		return nil, err
	}
	return GameSeatsTeamsTx(ctx, q, festID, gameID, doc.MultiFestVenueID)
}

// OwnRosterView is the roster tab of a flat Game that plays its separate
// list: its teams with their people. ok is false for a Game that plays the
// fest's teams, whose tab is the fest roster's.
func OwnRosterView(ctx context.Context, q store.Queryer, gameID int64) (teams []FestRosterTeamView, ok bool, err error) {
	list, err := loadOwnList(ctx, q, gameID)
	if err != nil || !list.own {
		return nil, false, err
	}
	teams = make([]FestRosterTeamView, 0, len(list.teams))
	for _, team := range list.teams {
		view := FestRosterTeamView{Number: team.Number, Name: team.Name, City: team.City, RatingID: team.RatingID, Flags: team.Flags, Players: []FestRosterPlayerView{}}
		for _, player := range team.Players {
			view.Players = append(view.Players, FestRosterPlayerView{Name: player.Name, RatingID: player.RatingID})
		}
		teams = append(teams, view)
	}
	return teams, true, nil
}

// enteredNumbers is the numbers of the teams the Game holds something a host
// entered for.
func enteredNumbers(doc store.GameDoc) map[int64]bool {
	out := map[int64]bool{}
	counter, ok := games.As[games.Entered](doc.GameType)
	if !ok {
		return out
	}
	state := json.RawMessage(doc.State)
	entered, seats := counter.EnteredSeats(state), counter.Seats(state)
	for i, seat := range seats {
		if i < len(entered) && entered[i] {
			out[seat.Number] = true
		}
	}
	return out
}

// SetOwnTeamsTx has a flat Game play its separate list (own) or the fest's
// teams. The first time it plays a list, the list is the teams it seats then.
func SetOwnTeamsTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, own bool) (GameStateBroadcast, error) {
	doc, err := flatGame(ctx, tx, festID, gameID)
	if err != nil {
		return GameStateBroadcast{}, err
	}
	list, err := loadOwnList(ctx, tx, gameID)
	if err != nil {
		return GameStateBroadcast{}, err
	}
	if own && list.teams == nil {
		seated, err := festSeated(ctx, tx, festID, doc.MultiFestVenueID)
		if err != nil {
			return GameStateBroadcast{}, err
		}
		list.teams = NumberedOwnTeams(OwnTeams(seated))
	}
	list.own = own
	if err := storeOwnList(ctx, tx, gameID, list); err != nil {
		return GameStateBroadcast{}, err
	}
	return refoldTx(ctx, tx, festID, doc, Remap{})
}

// SaveOwnTeamsTx replaces a flat Game's separate list. Every team needs a
// name and a number of its own, and a team the Game holds answers for stays
// (its Prev names it), its answers following it to a new number.
func SaveOwnTeamsTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, teams []OwnTeam) (GameStateBroadcast, error) {
	doc, err := flatGame(ctx, tx, festID, gameID)
	if err != nil {
		return GameStateBroadcast{}, err
	}
	list, err := loadOwnList(ctx, tx, gameID)
	if err != nil {
		return GameStateBroadcast{}, err
	}
	if !list.own {
		return GameStateBroadcast{}, corei18n.User(dopestrings.Default.Entrants.Own.Follows())
	}
	teams, err = checkOwnTeams(teams, list.teams, enteredNumbers(doc))
	if err != nil {
		return GameStateBroadcast{}, err
	}
	numbers := map[int]int{}
	for _, team := range teams {
		if team.Prev > 0 && team.Prev != team.Number {
			numbers[int(team.Prev)] = int(team.Number)
		}
	}
	list.teams = teams
	if err := storeOwnList(ctx, tx, gameID, list); err != nil {
		return GameStateBroadcast{}, err
	}
	return refoldTx(ctx, tx, festID, doc, Remap{Venue: doc.MultiFestVenueID, Numbers: numbers})
}

// checkOwnTeams tidies a separate list a host sent and refuses one without a
// name or a number, with a number twice, or without a team the Game holds
// answers for (before, the list as stored, names it).
func checkOwnTeams(teams, before []OwnTeam, entered map[int64]bool) ([]OwnTeam, error) {
	out := make([]OwnTeam, 0, len(teams))
	byNumber := map[int64]string{}
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
		byNumber[team.Number] = team.Name
		kept[team.Prev] = true
		out = append(out, team)
	}
	for _, team := range before {
		if entered[team.Number] && !kept[team.Number] {
			return nil, corei18n.User(dopestrings.Default.Entrants.Own.Entered(team.Name))
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

// refoldTx seats the Game's teams as they are now.
func refoldTx(ctx context.Context, tx *sql.Tx, festID int64, doc store.GameDoc, remap Remap) (GameStateBroadcast, error) {
	teams, err := GameSeatsTeamsTx(ctx, tx, festID, doc.GameID, doc.MultiFestVenueID)
	if err != nil {
		return GameStateBroadcast{}, err
	}
	return foldGameTx(ctx, tx, festID, doc, RosterTeams(teams), remap)
}
