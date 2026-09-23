package roster

import (
	"context"
	"database/sql"
	"errors"
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
	// Team is the fest team holding at least two of its players — the one a
	// troika's place is credited to (regulations VII.2.1) — or "".
	Team string
	// Seated reports whether a Game seats it; such a team cannot be deleted.
	Seated bool
}

// AssembledInput is a team as a host typed it: a name and its people's names.
type AssembledInput struct {
	Name    string
	Players []string
}

// LoadAssembled lists the fest's assembled teams by name, each with its people
// in roster order, the team it is credited to and whether a Game seats it.
func LoadAssembled(ctx context.Context, q store.Queryer, festID int64) ([]Assembled, error) {
	teams, err := store.CollectRows(ctx, q, `
select p.id, p.name, exists(select 1 from game_participants gp where gp.participant_id = p.id)
    or exists(select 1 from match_slots ms where ms.participant_id = p.id)
from participants p
where p.fest_id = ? and p.assembled = 1
order by p.name collate nocase, p.id`, []any{festID}, func(rows *sql.Rows) (Assembled, error) {
		var team Assembled
		return team, rows.Scan(&team.ID, &team.Name, &team.Seated)
	})
	if err != nil {
		return nil, err
	}
	type member struct {
		team        int64
		first, last string
	}
	members, err := store.CollectRows(ctx, q, `
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
	teamsOf, err := festTeamsByPlayerName(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*Assembled{}
	for i := range teams {
		byID[teams[i].ID] = &teams[i]
	}
	counts := map[int64]map[string]int{}
	for _, m := range members {
		team := byID[m.team]
		if team == nil {
			continue
		}
		team.Players = append(team.Players, store.JoinPlayerName(m.first, m.last))
		if counts[m.team] == nil {
			counts[m.team] = map[string]int{}
		}
		for _, festTeam := range teamsOf[nameKey(m.first, m.last)] {
			counts[m.team][festTeam]++
		}
	}
	for id, byTeam := range counts {
		var credited []string
		for festTeam, n := range byTeam {
			if n >= 2 {
				credited = append(credited, festTeam)
			}
		}
		sort.Strings(credited)
		byID[id].Team = strings.Join(credited, ", ")
	}
	return teams, nil
}

// festTeamsByPlayerName maps a person's name to the fest teams it plays for in
// the rating roster.
func festTeamsByPlayerName(ctx context.Context, q store.Queryer, festID int64) (map[string][]string, error) {
	rows, err := store.CollectRows(ctx, q, `
select fp.first_name, fp.last_name, t.name
from fest_players fp
join fest_team_players ftp on ftp.player_id = fp.id
join fest_teams t on t.id = ftp.team_id and t.deleted = 0
where fp.fest_id = ?`, []any{festID}, func(rows *sql.Rows) ([3]string, error) {
		var r [3]string
		return r, rows.Scan(&r[0], &r[1], &r[2])
	})
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, r := range rows {
		key := nameKey(r[0], r[1])
		out[key] = append(out[key], r[2])
	}
	return out, nil
}

func nameKey(first, last string) string {
	return util.AlphaKey(store.JoinPlayerName(first, last))
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

// SaveAssembledTx creates an assembled team (id 0) or rewrites one: its name
// and its people, in the order given. A name another Participant already goes
// by is refused — the fest's other lookups find Participants by name — and so
// is a roster outside two to four people.
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
select id from participants where fest_id = ? and name = ? and id != ? limit 1`, festID, name, id).Scan(&clash)
	switch {
	case err == nil:
		return 0, corei18n.User(s.Host.Troikas.NameTaken(name))
	case !errors.Is(err, sql.ErrNoRows):
		return 0, err
	}
	if id == 0 {
		if id, err = store.InsertReturningID(ctx, tx, `
insert into participants(fest_id, roster, name, city, assembled) values(?, 'team', ?, '', 1)`, festID, name); err != nil {
			return 0, err
		}
	} else {
		result, err := tx.ExecContext(ctx, `
update participants set name = ? where id = ? and fest_id = ? and assembled = 1`, name, id, festID)
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
