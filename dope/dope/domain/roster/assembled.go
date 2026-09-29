package roster

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"

	"dope/dope/platform/util"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// An assembled team (CONTEXT.md, Assembled team) is a Participant put together
// for one format out of fest players — a troika — rather than drawn from the
// rating roster. It lives in the fest's participants with assembled = 1, its
// people are participant_players rows, and a bracket Game lists it in the
// entrant picker. The flat games never see it: they seat the fest roster.
//
// It carries a head team, the fest team its places count for, and a division.
// A new troika takes the team that holds at least two of its players and
// follows that team's Flags; the host can pick another team, or none, and set
// the division apart from the team's. A Troika Game that declares a division seats
// the troikas in it.

// AssembledMin and AssembledMax are how many people a troika declares: two at
// the least, four at the most (regulations IV.1.2).
const (
	AssembledMin = 2
	AssembledMax = 4
)

// Assembled is one assembled team as the host page lists it.
type Assembled struct {
	ID      int64
	Name    string
	Players []string
	// HeadTeamID and HeadTeam are the fest team the troika's places count for
	// (regulations VII.2.1), 0 and "" for none.
	HeadTeamID int64
	HeadTeam   string
	// Division is the division the host chose, "" for none; nil while the troika
	// follows its head team's.
	Division *string
	// Flags is the division the troika plays in: its head team's Flags, or the
	// host's choice.
	Flags []string
	// Seated reports whether a Game seats it; such a team cannot be deleted.
	Seated bool
}

// FollowsTeam reports whether the troika's division is its head team's.
func (a Assembled) FollowsTeam() bool { return a.Division == nil }

// AssembledInput is a team as a host typed it: a name and its people's names,
// and, from the edit dialog, its head team and division.
type AssembledInput struct {
	Name    string
	Players []string
	// Placement is nil from the pasted lines: a new troika then takes its
	// derived head team and follows that team's division, and an edit keeps both.
	Placement *AssembledPlacement
}

// AssembledPlacement is what the edit dialog says about the troika's standing:
// the head team (0 for none) and the division (nil to follow the head team, ""
// for none).
type AssembledPlacement struct {
	HeadTeamID int64
	Division   *string
}

// LoadAssembled lists the fest's assembled teams by name, each with its people
// in roster order, its head team, its division and whether a Game seats it.
func LoadAssembled(ctx context.Context, q store.Queryer, festID int64) ([]Assembled, error) {
	teams, err := store.CollectRows(ctx, q, `
select p.id, p.name, coalesce(t.id, 0), coalesce(t.name, ''), p.division,
  exists(select 1 from game_participants gp where gp.participant_id = p.id)
    or exists(select 1 from match_slots ms where ms.participant_id = p.id)
from participants p
left join fest_teams t on t.id = p.head_team_id and t.fest_id = p.fest_id and t.deleted = 0
where p.fest_id = ? and p.assembled = 1
order by p.name collate nocase, p.id`, []any{festID}, func(rows *sql.Rows) (Assembled, error) {
		var team Assembled
		var division sql.NullString
		if err := rows.Scan(&team.ID, &team.Name, &team.HeadTeamID, &team.HeadTeam, &division, &team.Seated); err != nil {
			return team, err
		}
		if division.Valid {
			team.Division = &division.String
		}
		return team, nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(teams, func(i, j int) bool { return util.CompareNatural(teams[i].Name, teams[j].Name) < 0 })
	members, err := assembledMembers(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	flags, err := FestTeamFlags(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	for i := range teams {
		team := &teams[i]
		for _, m := range members[team.ID] {
			team.Players = append(team.Players, store.JoinPlayerName(m[0], m[1]))
		}
		switch {
		case team.Division == nil:
			team.Flags = flags[team.HeadTeamID]
		case *team.Division != "":
			team.Flags = []string{*team.Division}
		}
	}
	return teams, nil
}

// assembledMembers maps each of the fest's assembled teams to its people's
// first and last names, in roster order.
func assembledMembers(ctx context.Context, q store.Queryer, festID int64) (map[int64][][2]string, error) {
	type member struct {
		team        int64
		first, last string
	}
	rows, err := store.CollectRows(ctx, q, `
select pp.participant_id, pl.first_name, pl.last_name
from participant_players pp
join participants p on p.id = pp.participant_id
join players pl on pl.id = pp.player_id
where p.fest_id = ? and p.assembled = 1
order by pp.participant_id, pp.roster_order`, []any{festID}, func(rows *sql.Rows) (member, error) {
		var m member
		return m, rows.Scan(&m.team, &m.first, &m.last)
	})
	if err != nil {
		return nil, err
	}
	out := map[int64][][2]string{}
	for _, m := range rows {
		out[m.team] = append(out[m.team], [2]string{m.first, m.last})
	}
	return out, nil
}

// FestTeamFlags maps each of the fest's teams to its Flags' short names, in the
// order the team lists them.
func FestTeamFlags(ctx context.Context, q store.Queryer, festID int64) (map[int64][]string, error) {
	type flag struct {
		team  int64
		short string
	}
	rows, err := store.CollectRows(ctx, q, `
select f.team_id, f.short from fest_team_flags f
join fest_teams t on t.id = f.team_id
where t.fest_id = ?
order by f.team_id, f.position`, []any{festID}, func(rows *sql.Rows) (flag, error) {
		var f flag
		return f, rows.Scan(&f.team, &f.short)
	})
	if err != nil {
		return nil, err
	}
	out := map[int64][]string{}
	for _, f := range rows {
		out[f.team] = append(out[f.team], f.short)
	}
	return out, nil
}

// DerivedHeadTeams maps each of the fest's assembled teams to the one fest team
// that holds at least two of its players, which is the team its places count
// for (regulations VII.2.1). A troika with no such team, or with two, is left
// out: nothing decides between them.
func DerivedHeadTeams(ctx context.Context, q store.Queryer, festID int64) (map[int64]int64, error) {
	members, err := assembledMembers(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	teamsOf, err := festTeamsByPlayerName(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	out := map[int64]int64{}
	for troika, people := range members {
		if team, ok := derivedHeadTeam(people, teamsOf); ok {
			out[troika] = team
		}
	}
	return out, nil
}

func derivedHeadTeam(people [][2]string, teamsOf map[string][]int64) (int64, bool) {
	counts := map[int64]int{}
	for _, p := range people {
		for _, team := range teamsOf[nameKey(p[0], p[1])] {
			counts[team]++
		}
	}
	var found []int64
	for team, n := range counts {
		if n >= 2 {
			found = append(found, team)
		}
	}
	if len(found) != 1 {
		return 0, false
	}
	return found[0], true
}

// festTeamsByPlayerName maps a person's name to the fest teams it plays for in
// the rating roster.
func festTeamsByPlayerName(ctx context.Context, q store.Queryer, festID int64) (map[string][]int64, error) {
	type row struct {
		first, last string
		team        int64
	}
	rows, err := store.CollectRows(ctx, q, `
select fp.first_name, fp.last_name, t.id
from fest_players fp
join fest_team_players ftp on ftp.player_id = fp.id
join fest_teams t on t.id = ftp.team_id and t.deleted = 0
where fp.fest_id = ?`, []any{festID}, func(rows *sql.Rows) (row, error) {
		var r row
		return r, rows.Scan(&r.first, &r.last, &r.team)
	})
	if err != nil {
		return nil, err
	}
	out := map[string][]int64{}
	for _, r := range rows {
		key := nameKey(r.first, r.last)
		out[key] = append(out[key], r.team)
	}
	return out, nil
}

func nameKey(first, last string) string {
	return util.AlphaKey(store.JoinPlayerName(first, last))
}

// InDivision reports whether a Participant with these Flags plays in a
// Division as a scheme writes it: a Flag it carries, or with a leading minus a
// Flag it does not carry (ADR-0020).
func InDivision(flags []string, division string) bool {
	flag, exclude := strings.CutPrefix(strings.TrimSpace(division), "-")
	return slices.Contains(flags, strings.TrimSpace(flag)) != exclude
}

// AssembledInDivision lists the fest's assembled teams that play in a Division,
// by name, leaving out the one excluded (a troika about to be deleted).
func AssembledInDivision(ctx context.Context, q store.Queryer, festID int64, division string, exclude int64) ([]Assembled, error) {
	all, err := LoadAssembled(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	var out []Assembled
	for _, team := range all {
		if team.ID != exclude && InDivision(team.Flags, division) {
			out = append(out, team)
		}
	}
	return out, nil
}

// GameEntrantView is one Participant a Game seats, for the game page's
// roster tab: its number in the Game, its name and people, and for a troika
// the team it counts for and its division.
type GameEntrantView struct {
	Number   int64                  `json:"number,omitempty"`
	Name     string                 `json:"name"`
	City     string                 `json:"city,omitempty"`
	Players  []FestRosterPlayerView `json:"players"`
	HeadTeam string                 `json:"headTeam,omitempty"`
	Flags    []string               `json:"flags,omitempty"`
}

// LoadGameEntrantsView lists the Participants a Game seats, in its own order.
// It is empty for a Game that never named its entrants, which seats the fest
// roster instead.
func LoadGameEntrantsView(ctx context.Context, q store.Queryer, festID, gameID int64) ([]GameEntrantView, error) {
	type entrant struct {
		id   int64
		view GameEntrantView
	}
	entrants, err := store.CollectRows(ctx, q, `
select p.id, coalesce(gp.number, 0), p.name, coalesce(p.city, '')
from game_participants gp
join participants p on p.id = gp.participant_id and p.fest_id = ?
where gp.game_id = ?
order by gp.position`, []any{festID, gameID}, func(rows *sql.Rows) (entrant, error) {
		var e entrant
		return e, rows.Scan(&e.id, &e.view.Number, &e.view.Name, &e.view.City)
	})
	if err != nil || len(entrants) == 0 {
		return nil, err
	}
	type member struct {
		participant int64
		first, last string
	}
	members, err := store.CollectRows(ctx, q, `
select pp.participant_id, pl.first_name, pl.last_name
from participant_players pp
join players pl on pl.id = pp.player_id
where pp.participant_id in (select participant_id from game_participants where game_id = ?)
order by pp.participant_id, pp.roster_order`, []any{gameID}, func(rows *sql.Rows) (member, error) {
		var m member
		return m, rows.Scan(&m.participant, &m.first, &m.last)
	})
	if err != nil {
		return nil, err
	}
	people := map[int64][]FestRosterPlayerView{}
	for _, m := range members {
		people[m.participant] = append(people[m.participant], FestRosterPlayerView{Name: store.JoinPlayerName(m.first, m.last)})
	}
	assembled, err := LoadAssembled(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	troikas := map[int64]Assembled{}
	for _, a := range assembled {
		troikas[a.ID] = a
	}
	out := make([]GameEntrantView, len(entrants))
	for i, e := range entrants {
		view := e.view
		view.Players = people[e.id]
		if view.Players == nil {
			view.Players = []FestRosterPlayerView{}
		}
		if troika, ok := troikas[e.id]; ok {
			view.HeadTeam, view.Flags = troika.HeadTeam, troika.Flags
		}
		out[i] = view
	}
	return out, nil
}

// FestTeamChoice is one fest team as the troika dialog offers it for a head
// team.
type FestTeamChoice struct {
	ID    int64
	Name  string
	Flags []string
}

// LoadFestTeamChoices lists the fest's teams by name, with their Flags.
func LoadFestTeamChoices(ctx context.Context, q store.Queryer, festID int64) ([]FestTeamChoice, error) {
	teams, err := store.CollectRows(ctx, q, `
select id, name from fest_teams where fest_id = ? and deleted = 0 order by name collate nocase, id`,
		[]any{festID}, func(rows *sql.Rows) (FestTeamChoice, error) {
			var team FestTeamChoice
			return team, rows.Scan(&team.ID, &team.Name)
		})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(teams, func(i, j int) bool { return util.CompareNatural(teams[i].Name, teams[j].Name) < 0 })
	flags, err := FestTeamFlags(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	for i := range teams {
		teams[i].Flags = flags[teams[i].ID]
	}
	return teams, nil
}

// FestPlayerChoice is one fest player as the troika form offers them: the name
// to type, and the team it plays for to tell namesakes apart.
type FestPlayerChoice struct {
	Name string
	Team string
}

// LoadFestPlayerChoices lists the fest's players by name for the troika form.
func LoadFestPlayerChoices(ctx context.Context, q store.Queryer, festID int64) ([]FestPlayerChoice, error) {
	return store.CollectRows(ctx, q, `
select fp.first_name, fp.last_name, coalesce((
  select t.name from fest_team_players ftp join fest_teams t on t.id = ftp.team_id and t.deleted = 0
  where ftp.player_id = fp.id order by ftp.roster_order limit 1), '')
from fest_players fp
where fp.fest_id = ?
order by fp.last_name collate nocase, fp.first_name collate nocase, fp.id`, []any{festID}, func(rows *sql.Rows) (FestPlayerChoice, error) {
		var first, last string
		var choice FestPlayerChoice
		if err := rows.Scan(&first, &last, &choice.Team); err != nil {
			return choice, err
		}
		choice.Name = store.JoinPlayerName(first, last)
		return choice, nil
	})
}

// SplitPlayerName reads a typed person: first name then surname, as the
// rating roster joins them, with anything after an opening bracket dropped —
// the form's suggestions carry the team there. A single word is a surname.
func SplitPlayerName(typed string) (first, last string) {
	if i := strings.Index(typed, "("); i >= 0 {
		typed = typed[:i]
	}
	words := strings.Fields(typed)
	switch len(words) {
	case 0:
		return "", ""
	case 1:
		return "", words[0]
	}
	return words[0], strings.Join(words[1:], " ")
}

// ParseAssembledLines reads the bulk form, one team a line: its name, a colon,
// and its people separated by commas. Blank lines are skipped, and a line
// without a name before a colon is refused with its number.
func ParseAssembledLines(text string) ([]AssembledInput, error) {
	s := dopestrings.Default
	var out []AssembledInput
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, people, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, corei18n.User(s.Host.Troikas.LineNoName(strconv.Itoa(i + 1)))
		}
		var players []string
		for _, p := range strings.Split(people, ",") {
			if p = strings.TrimSpace(p); p != "" {
				players = append(players, p)
			}
		}
		out = append(out, AssembledInput{Name: strings.TrimSpace(name), Players: players})
	}
	return out, nil
}

// SaveAssembledTx creates an assembled team (id 0) or rewrites one: its name,
// its people in the order given and, when the input carries a placement, its
// head team and division. A new team without a placement takes its derived head
// team and follows it. A name another Participant already goes by is refused —
// the fest's other lookups find Participants by name — and so is a roster
// outside two to four people, or a head team the fest does not have.
func SaveAssembledTx(ctx context.Context, tx *sql.Tx, festID, id int64, in AssembledInput) (int64, error) {
	s := dopestrings.Default
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return 0, corei18n.User(s.Host.Troikas.NameMissing())
	}
	var players [][2]string
	seen := map[string]bool{}
	for _, typed := range in.Players {
		first, last := SplitPlayerName(typed)
		if first == "" && last == "" {
			continue
		}
		key := nameKey(first, last)
		if seen[key] {
			return 0, corei18n.User(s.Host.Troikas.PlayerTwice(name, store.JoinPlayerName(first, last)))
		}
		seen[key] = true
		players = append(players, [2]string{first, last})
	}
	if len(players) < AssembledMin || len(players) > AssembledMax {
		return 0, corei18n.User(s.Host.Troikas.RosterSize(name, len(players)))
	}
	var clash int64
	err := tx.QueryRowContext(ctx, `
select id from participants where fest_id = ? and name = ? and id != ? and game_id is null limit 1`, festID, name, id).Scan(&clash)
	switch {
	case err == nil:
		return 0, corei18n.User(s.Host.Troikas.NameTaken(name))
	case !errors.Is(err, sql.ErrNoRows):
		return 0, err
	}
	placement := in.Placement
	if placement == nil && id == 0 {
		teamsOf, err := festTeamsByPlayerName(ctx, tx, festID)
		if err != nil {
			return 0, err
		}
		team, _ := derivedHeadTeam(players, teamsOf)
		placement = &AssembledPlacement{HeadTeamID: team}
	}
	var headTeam, division any
	if placement != nil {
		if placement.HeadTeamID > 0 {
			var found int64
			err := tx.QueryRowContext(ctx, `
select id from fest_teams where id = ? and fest_id = ? and deleted = 0`, placement.HeadTeamID, festID).Scan(&found)
			if errors.Is(err, sql.ErrNoRows) {
				return 0, corei18n.User(s.Host.Troikas.HeadTeamUnknown())
			}
			if err != nil {
				return 0, err
			}
			headTeam = placement.HeadTeamID
		}
		if placement.Division != nil {
			division = strings.TrimSpace(*placement.Division)
		}
	}
	switch {
	case id == 0:
		if id, err = store.InsertReturningID(ctx, tx, `
insert into participants(fest_id, roster, name, city, assembled, head_team_id, division) values(?, 'team', ?, '', 1, ?, ?)`,
			festID, name, headTeam, division); err != nil {
			return 0, err
		}
	default:
		query, args := `
update participants set name = ? where id = ? and fest_id = ? and assembled = 1`, []any{name, id, festID}
		if placement != nil {
			query, args = `
update participants set name = ?, head_team_id = ?, division = ? where id = ? and fest_id = ? and assembled = 1`,
				[]any{name, headTeam, division, id, festID}
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return 0, err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return 0, sql.ErrNoRows
		}
	}
	seed := make([]SeedRosterPlayer, len(players))
	for i, p := range players {
		seed[i] = SeedRosterPlayer{FirstName: p[0], LastName: p[1]}
	}
	return id, ReplaceSeedTeamRoster(ctx, tx, festID, id, seed)
}

// DeleteAssembledTx removes an assembled team no Game seats.
func DeleteAssembledTx(ctx context.Context, tx *sql.Tx, festID, id int64) error {
	var seated bool
	if err := tx.QueryRowContext(ctx, `
select exists(select 1 from game_participants where participant_id = ?)
    or exists(select 1 from match_slots where participant_id = ?)
    or exists(select 1 from game_assignments where participant_id = ?)`, id, id, id).Scan(&seated); err != nil {
		return err
	}
	if seated {
		return corei18n.User(dopestrings.Default.Host.Troikas.DeleteSeated())
	}
	if _, err := tx.ExecContext(ctx, `delete from participant_players where participant_id = ?`, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `delete from participants where id = ? and fest_id = ? and assembled = 1`, id, festID)
	return err
}
