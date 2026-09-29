package roster

import (
	"context"
	"database/sql"
	"sort"
	"strconv"
	"strings"

	"dope/dope/storage/store"
)

// The fest roster is the rating import with the host's edits on top
// (ADR-0024). This file holds what an edit is, how the edits are read back, and
// MergeHand, the one rule that lays them over an incoming roster. The import,
// a hand edit and an undo all compute the roster they write through it.

// PlayerKey identifies a player across imports: the rating id, or the name for
// a person the rating site does not know.
func PlayerKey(p FestRosterImportPlayer) string {
	if p.RatingID > 0 {
		return "rating:" + strconv.FormatInt(p.RatingID, 10)
	}
	return "name:" + strings.ToLower(store.JoinPlayerName(p.FirstName, p.LastName))
}

// HandTeam is one fest_teams row as the merge reads it: what it is now and
// which of it the host set.
type HandTeam struct {
	ID       int64
	RatingID int64
	Number   int64
	Name     string
	City     string
	Country  string
	// Hand marks a team the host made; the import never touches it.
	Hand bool
	// HandName and HandCity are the name and city the host gave a rating team,
	// which win over the import's; nil follows the import.
	HandName *string
	HandCity *string
	// HandFlags marks Flags the host typed, which the import leaves alone.
	HandFlags bool
	// HandRemoved marks a rating team the host took off: an import that still
	// lists it does not bring it back.
	HandRemoved bool
	Deleted     bool
	Flags       []FestRosterFlag
	// Players are the team's people now, in roster order.
	Players []FestRosterImportPlayer
}

// HandEdit is one player the host added to a team, or took off it.
type HandEdit struct {
	TeamID int64
	Player FestRosterImportPlayer
	Remove bool
}

// HandState is a fest's roster rows and the host's player edits.
type HandState struct {
	Teams []HandTeam
	Edits []HandEdit
}

// PlayerConflict is a player the site now puts on a team while the host had
// placed them on another by hand. Key names it in a form and in AcceptSite.
type PlayerConflict struct {
	Key        string
	Player     FestRosterImportPlayer
	HandTeamID int64
	HandTeam   string
	SiteTeamID int64
	SiteTeam   string
}

// HandKept counts the host's edits a merge kept: what the import page says it
// did not overwrite.
type HandKept struct {
	HandTeams    int
	Renamed      int
	Flags        int
	RemovedTeams int
	Added        int
	Removed      int
}

// MergeResult is a merged roster and what the merge had to decide.
type MergeResult struct {
	Desired   []FestRosterImportTeam
	Conflicts []PlayerConflict
	// Settle are the edits that make the host's answers to the conflicts
	// stick, to write with the roster: a remove where the host's placement
	// stood, and the dropped add (a remove of the edit) where the site's did.
	Settle []HandEdit
	Drop   []HandEdit
	Kept   HandKept
}

// ConflictKey is a conflict's name: the team the site puts the player on and
// the player.
func ConflictKey(siteTeamID int64, p FestRosterImportPlayer) string {
	return strconv.FormatInt(siteTeamID, 10) + "|" + PlayerKey(p)
}

// MergeHand lays the host's edits over an incoming roster. For every rating
// team the incoming one lists, the players are the site's, less those the host
// took off, plus those the host added; the name, city and Flags are the host's
// where the host set them. A rating team the host took off stays off. A team
// the host made is kept as it is. Teams keep their rows by LocalID, so the
// writer updates in place. A player the site places on a team they were not on,
// while the host had placed them elsewhere by hand, is a conflict: the host's
// placement stands unless acceptSite names it.
func MergeHand(incoming []FestRosterImportTeam, state HandState, acceptSite map[string]bool) MergeResult {
	var result MergeResult
	byRating := map[int64]HandTeam{}
	byID := map[int64]HandTeam{}
	for _, team := range state.Teams {
		byID[team.ID] = team
		if team.RatingID > 0 {
			byRating[team.RatingID] = team
		}
	}
	adds := map[int64][]FestRosterImportPlayer{}
	removes := map[int64]map[string]bool{}
	placedAt := map[string][]int64{}
	for _, edit := range state.Edits {
		key := PlayerKey(edit.Player)
		if edit.Remove {
			if removes[edit.TeamID] == nil {
				removes[edit.TeamID] = map[string]bool{}
			}
			removes[edit.TeamID][key] = true
			result.Kept.Removed++
			continue
		}
		adds[edit.TeamID] = append(adds[edit.TeamID], edit.Player)
		placedAt[key] = append(placedAt[key], edit.TeamID)
		result.Kept.Added++
	}
	onTeam := func(team HandTeam, key string) bool {
		for _, p := range team.Players {
			if PlayerKey(p) == key {
				return true
			}
		}
		return false
	}

	// First the conflicts, since a site placement the host accepts takes the
	// player out of the team the host had put them on.
	dropped := map[int64]map[string]bool{}
	skip := map[int64]map[string]bool{}
	for _, team := range incoming {
		row, known := byRating[team.RatingID]
		if team.RatingID <= 0 || (known && row.HandRemoved) {
			continue
		}
		for _, p := range team.Players {
			key := PlayerKey(p)
			if known && (removes[row.ID][key] || onTeam(row, key)) {
				continue
			}
			for _, handTeamID := range placedAt[key] {
				if known && handTeamID == row.ID {
					continue
				}
				handTeam := byID[handTeamID]
				if handTeam.Deleted {
					continue
				}
				conflict := PlayerConflict{
					Player: p, HandTeamID: handTeamID, HandTeam: handTeam.Name,
					SiteTeamID: row.ID, SiteTeam: team.Name,
				}
				conflict.Key = ConflictKey(row.ID, p)
				if !known {
					conflict.Key = "rating:" + strconv.FormatInt(team.RatingID, 10) + "|" + key
				}
				result.Conflicts = append(result.Conflicts, conflict)
				if acceptSite[conflict.Key] {
					if dropped[handTeamID] == nil {
						dropped[handTeamID] = map[string]bool{}
					}
					dropped[handTeamID][key] = true
					result.Drop = append(result.Drop, HandEdit{TeamID: handTeamID, Player: p})
					continue
				}
				if skip[team.RatingID] == nil {
					skip[team.RatingID] = map[string]bool{}
				}
				skip[team.RatingID][key] = true
				if known {
					result.Settle = append(result.Settle, HandEdit{TeamID: row.ID, Player: p, Remove: true})
				}
			}
		}
	}

	withAdds := func(teamID int64, players []FestRosterImportPlayer) []FestRosterImportPlayer {
		seen := map[string]bool{}
		for _, p := range players {
			seen[PlayerKey(p)] = true
		}
		for _, p := range adds[teamID] {
			key := PlayerKey(p)
			if seen[key] || dropped[teamID][key] {
				continue
			}
			seen[key] = true
			players = append(players, p)
		}
		return players
	}

	for _, team := range incoming {
		row, known := byRating[team.RatingID]
		if known && row.HandRemoved {
			result.Kept.RemovedTeams++
			continue
		}
		out := team
		out.Players = nil
		for _, p := range team.Players {
			key := PlayerKey(p)
			if skip[team.RatingID][key] || (known && removes[row.ID][key]) {
				continue
			}
			out.Players = append(out.Players, p)
		}
		if known {
			out.LocalID = row.ID
			if row.HandName != nil {
				out.Name = *row.HandName
				result.Kept.Renamed++
			}
			if row.HandCity != nil {
				out.City = *row.HandCity
			}
			if row.HandFlags {
				out.Flags = row.Flags
				result.Kept.Flags++
			}
			out.Players = withAdds(row.ID, out.Players)
		}
		result.Desired = append(result.Desired, out)
	}
	for _, row := range state.Teams {
		if !row.Hand || row.Deleted {
			continue
		}
		result.Kept.HandTeams++
		result.Desired = append(result.Desired, FestRosterImportTeam{
			LocalID: row.ID, Name: row.Name, City: row.City, Country: row.Country,
			Number: row.Number, Flags: row.Flags, Players: withAdds(row.ID, nil),
		})
	}
	sort.SliceStable(result.Conflicts, func(i, j int) bool { return result.Conflicts[i].Key < result.Conflicts[j].Key })
	return result
}

// Baseline is the roster the site last gave, worked back from the rows and the
// edits: each rating team's players less those the host added, plus those the
// host took off. A hand edit merges its new edits over it, so the result is
// what an import of that same roster would now give.
func Baseline(state HandState) []FestRosterImportTeam {
	adds := map[int64]map[string]bool{}
	removes := map[int64][]FestRosterImportPlayer{}
	for _, edit := range state.Edits {
		if edit.Remove {
			removes[edit.TeamID] = append(removes[edit.TeamID], edit.Player)
			continue
		}
		if adds[edit.TeamID] == nil {
			adds[edit.TeamID] = map[string]bool{}
		}
		adds[edit.TeamID][PlayerKey(edit.Player)] = true
	}
	var out []FestRosterImportTeam
	for _, row := range state.Teams {
		if row.Hand || row.RatingID <= 0 || (row.Deleted && !row.HandRemoved) {
			continue
		}
		team := FestRosterImportTeam{RatingID: row.RatingID, Name: row.Name, City: row.City,
			Country: row.Country, Number: row.Number, Flags: row.Flags}
		for _, p := range row.Players {
			if !adds[row.ID][PlayerKey(p)] {
				team.Players = append(team.Players, p)
			}
		}
		team.Players = append(team.Players, removes[row.ID]...)
		out = append(out, team)
	}
	return out
}

// LoadHandState reads a fest's roster rows, deleted ones included, with their
// players and Flags, and the host's player edits.
func LoadHandState(ctx context.Context, q store.Queryer, festID int64) (HandState, error) {
	teams, err := store.CollectRows(ctx, q, `
select id, coalesce(rating_id, 0), coalesce(number, 0), name, coalesce(city, ''), coalesce(country, ''),
       hand, hand_name, hand_city, hand_flags, hand_removed, deleted
from fest_teams where fest_id = ? order by position, id`, []any{festID}, func(rows *sql.Rows) (HandTeam, error) {
		var t HandTeam
		var name, city sql.NullString
		if err := rows.Scan(&t.ID, &t.RatingID, &t.Number, &t.Name, &t.City, &t.Country,
			&t.Hand, &name, &city, &t.HandFlags, &t.HandRemoved, &t.Deleted); err != nil {
			return t, err
		}
		if name.Valid {
			t.HandName = &name.String
		}
		if city.Valid {
			t.HandCity = &city.String
		}
		return t, nil
	})
	if err != nil {
		return HandState{}, err
	}
	flags, err := LoadFestTeamFlags(ctx, q, festID)
	if err != nil {
		return HandState{}, err
	}
	type link struct {
		team   int64
		player FestRosterImportPlayer
	}
	links, err := store.CollectRows(ctx, q, `
select ftp.team_id, coalesce(p.rating_id, 0), p.first_name, p.last_name
from fest_team_players ftp
join fest_players p on p.id = ftp.player_id
join fest_teams t on t.id = ftp.team_id
where t.fest_id = ?
order by ftp.team_id, ftp.roster_order, p.id`, []any{festID}, func(rows *sql.Rows) (link, error) {
		var l link
		return l, rows.Scan(&l.team, &l.player.RatingID, &l.player.FirstName, &l.player.LastName)
	})
	if err != nil {
		return HandState{}, err
	}
	players := map[int64][]FestRosterImportPlayer{}
	for _, l := range links {
		players[l.team] = append(players[l.team], l.player)
	}
	for i := range teams {
		teams[i].Flags = flags[teams[i].ID]
		teams[i].Players = players[teams[i].ID]
	}
	edits, err := store.CollectRows(ctx, q, `
select team_id, coalesce(player_rating_id, 0), first_name, last_name, action
from fest_roster_edits where fest_id = ? order by id`, []any{festID}, func(rows *sql.Rows) (HandEdit, error) {
		var e HandEdit
		var action string
		if err := rows.Scan(&e.TeamID, &e.Player.RatingID, &e.Player.FirstName, &e.Player.LastName, &action); err != nil {
			return e, err
		}
		e.Remove = action == "remove"
		return e, nil
	})
	if err != nil {
		return HandState{}, err
	}
	return HandState{Teams: teams, Edits: edits}, nil
}

// SaveHandEditTx records one player edit, cancelling its opposite: adding a
// player the host had taken off the team only forgets the remove, and taking
// off a player the host had added only forgets the add.
func SaveHandEditTx(ctx context.Context, tx *sql.Tx, festID int64, edit HandEdit, now string) error {
	key := PlayerKey(edit.Player)
	existing, err := store.CollectRows(ctx, tx, `
select id, coalesce(player_rating_id, 0), first_name, last_name, action
from fest_roster_edits where fest_id = ? and team_id = ?`, []any{festID, edit.TeamID}, func(rows *sql.Rows) (struct {
		id     int64
		player FestRosterImportPlayer
		action string
	}, error) {
		var e struct {
			id     int64
			player FestRosterImportPlayer
			action string
		}
		return e, rows.Scan(&e.id, &e.player.RatingID, &e.player.FirstName, &e.player.LastName, &e.action)
	})
	if err != nil {
		return err
	}
	want := "add"
	if edit.Remove {
		want = "remove"
	}
	for _, e := range existing {
		if PlayerKey(e.player) != key {
			continue
		}
		if e.action == want {
			return nil
		}
		_, err := tx.ExecContext(ctx, `delete from fest_roster_edits where id = ?`, e.id)
		return err
	}
	_, err = tx.ExecContext(ctx, `
insert into fest_roster_edits(fest_id, team_id, player_rating_id, first_name, last_name, action, created_at)
values(?, ?, ?, ?, ?, ?, ?)`, festID, edit.TeamID, nullableRating(edit.Player.RatingID),
		strings.TrimSpace(edit.Player.FirstName), strings.TrimSpace(edit.Player.LastName), want, now)
	return err
}

func nullableRating(id int64) any {
	if id <= 0 {
		return nil
	}
	return id
}
