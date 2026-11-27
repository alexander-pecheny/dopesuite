package roster

import (
	"fmt"
	"strings"
)

// An override is one change a tethered Game makes to the teams its source
// gives it (ADR-0038), kept in order and applied again after every change of
// the source. It names a team by its key (teamKey) and a player by theirs
// (playerKey), so a sync from the rating site leaves it pointing at the same
// team. One whose team or player is gone is kept and reported as not applied.

// The kinds of override.
const (
	OverrideAddTeam      = "add_team"
	OverrideRemoveTeam   = "remove_team"
	OverrideRename       = "rename"
	OverrideCity         = "city"
	OverrideFlags        = "flags"
	OverrideNumber       = "number"
	OverrideAddPlayer    = "add_player"
	OverrideRemovePlayer = "remove_player"
	OverrideMovePlayer   = "move_player"
)

// Override is one override. Team is the key of the team it changes (the
// team it adds, for add_team); Label is that team's name when the override
// was made, which the tab shows when the team is gone. To and ToLabel are
// the team a moved player goes to.
type Override struct {
	ID      int64      `json:"id"`
	Kind    string     `json:"kind"`
	Team    string     `json:"team,omitempty"`
	Label   string     `json:"label,omitempty"`
	Name    string     `json:"name,omitempty"`
	City    string     `json:"city,omitempty"`
	Flags   []string   `json:"flags,omitempty"`
	Number  int64      `json:"number,omitempty"`
	Player  *OwnPlayer `json:"player,omitempty"`
	To      string     `json:"to,omitempty"`
	ToLabel string     `json:"toLabel,omitempty"`
	Add     *OwnTeam   `json:"add,omitempty"`
	// Applied is set when the Game's teams are worked out, never stored.
	Applied bool `json:"applied"`
}

// teamKey is a fest team's key: its rating site id, else its row.
func teamKey(team FestRosterImportTeam) string {
	if team.RatingID > 0 {
		return fmt.Sprintf("r%d", team.RatingID)
	}
	return fmt.Sprintf("t%d", team.LocalID)
}

// playerKey is a player's key: their rating site id, else their name.
func playerKey(player OwnPlayer) string {
	if player.RatingID > 0 {
		return fmt.Sprintf("r%d", player.RatingID)
	}
	return "n:" + strings.ToLower(strings.TrimSpace(player.Name))
}

// applyOverrides applies a Game's overrides in order to the teams its source
// gives it. It marks each override applied or not and each team with the
// fields an override changed.
func applyOverrides(teams []OwnTeam, overrides []Override) []OwnTeam {
	out := make([]OwnTeam, len(teams))
	copy(out, teams)
	for i := range overrides {
		var ok bool
		out, ok = applyOverride(out, overrides[i])
		overrides[i].Applied = ok
	}
	return out
}

func teamAt(teams []OwnTeam, key string) int {
	for i, team := range teams {
		if team.Key == key {
			return i
		}
	}
	return -1
}

func numberTaken(teams []OwnTeam, number int64, except int) bool {
	for i, team := range teams {
		if i != except && team.Number == number {
			return true
		}
	}
	return false
}

// applyOverride applies one override; ok is false when it cannot be.
func applyOverride(teams []OwnTeam, o Override) (out []OwnTeam, ok bool) {
	switch o.Kind {
	case OverrideAddTeam:
		return addTeam(teams, o)
	case OverrideMovePlayer:
		return movePlayer(teams, o)
	}
	at := teamAt(teams, o.Team)
	if at < 0 {
		return teams, false
	}
	if o.Kind == OverrideRemoveTeam {
		return append(teams[:at:at], teams[at+1:]...), true
	}
	team := teams[at]
	team.Players = append([]OwnPlayer(nil), team.Players...)
	if !changeTeam(&team, o, numberTaken(teams, o.Number, at)) {
		return teams, false
	}
	team.Overridden = append(append([]string(nil), team.Overridden...), o.Kind)
	teams[at] = team
	return teams, true
}

// changeTeam applies an override that changes one team; taken says the
// override's number is another team's.
func changeTeam(team *OwnTeam, o Override, taken bool) bool {
	switch o.Kind {
	case OverrideRename:
		team.Name = o.Name
	case OverrideCity:
		team.City = o.City
	case OverrideFlags:
		team.Flags = o.Flags
	case OverrideNumber:
		if o.Number <= 0 || taken {
			return false
		}
		team.Number = o.Number
	case OverrideAddPlayer:
		if o.Player == nil || hasPlayer(*team, *o.Player) {
			return false
		}
		team.Players = append(team.Players, *o.Player)
	case OverrideRemovePlayer:
		if o.Player == nil || !hasPlayer(*team, *o.Player) {
			return false
		}
		team.Players = withoutPlayer(team.Players, *o.Player)
	default:
		return false
	}
	return true
}

func hasPlayer(team OwnTeam, player OwnPlayer) bool {
	for _, p := range team.Players {
		if playerKey(p) == playerKey(player) {
			return true
		}
	}
	return false
}

func withoutPlayer(players []OwnPlayer, player OwnPlayer) []OwnPlayer {
	out := make([]OwnPlayer, 0, len(players))
	for _, p := range players {
		if playerKey(p) != playerKey(player) {
			out = append(out, p)
		}
	}
	return out
}

func addTeam(teams []OwnTeam, o Override) ([]OwnTeam, bool) {
	if o.Add == nil || teamAt(teams, o.Team) >= 0 || numberTaken(teams, o.Add.Number, -1) {
		return teams, false
	}
	team := *o.Add
	team.Key, team.Overridden = o.Team, []string{OverrideAddTeam}
	return append(teams, team), true
}

// movePlayer takes the player off whichever team has them and puts them on
// the override's team.
func movePlayer(teams []OwnTeam, o Override) ([]OwnTeam, bool) {
	to := teamAt(teams, o.To)
	if o.Player == nil || to < 0 {
		return teams, false
	}
	from := -1
	for i, team := range teams {
		if hasPlayer(team, *o.Player) {
			from = i
		}
	}
	if from == to {
		return teams, false
	}
	out := append([]OwnTeam(nil), teams...)
	if from >= 0 {
		out[from].Players = withoutPlayer(out[from].Players, *o.Player)
		out[from].Overridden = append(append([]string(nil), out[from].Overridden...), OverrideRemovePlayer)
	}
	out[to].Players = append(append([]OwnPlayer(nil), out[to].Players...), *o.Player)
	out[to].Overridden = append(append([]string(nil), out[to].Overridden...), OverrideAddPlayer)
	return out, true
}
