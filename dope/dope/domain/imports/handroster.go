package imports

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"dope/dope/domain/games"
	"dope/dope/domain/overrides"
	"dope/dope/domain/roster"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"
	corei18n "pecheny.me/dopecore/i18nstrings"
)

// The host's edits to the fest roster (ADR-0024), and the one write path an
// import, a hand edit and an undo share.

// ImportPlan is what an import does to the fest roster, for the host to read
// before confirming: the teams it adds, drops and renames, the players it adds
// and removes per team, the host's edits it keeps, and the conflicts it
// settles.
type ImportPlan struct {
	AddedTeams   []string
	DroppedTeams []string
	Renamed      []TeamRename
	Players      []TeamPlayers
	Kept         roster.HandKept
	Conflicts    []roster.PlayerConflict
}

type TeamRename struct {
	From string
	To   string
}

type TeamPlayers struct {
	Team    string
	Added   []string
	Removed []string
}

// Empty reports whether the plan changes nothing and asks nothing.
func (p ImportPlan) Empty() bool {
	return len(p.AddedTeams)+len(p.DroppedTeams)+len(p.Renamed)+len(p.Players)+len(p.Conflicts) == 0
}

// RosterWrite is what writing a roster changed, for the caller to broadcast.
type RosterWrite struct {
	Updates   []roster.GameStateBroadcast
	EKGameIDs []int64
	odGames   int
	ksiGames  int
}

// writeRosterTx writes a whole fest roster: the rows that changed, the flat
// games when the teams changed, and the player overrides against the rows
// that stay.
func writeRosterTx(ctx context.Context, tx *sql.Tx, festID int64, teams []roster.FestRosterImportTeam, existingByRating map[int64]existingFestTeam, merges map[int64]int64, teamsChanged bool) (RosterWrite, error) {
	var out RosterWrite
	if err := applyFestRosterDiffTx(ctx, tx, festID, teams, existingByRating, merges); err != nil {
		return out, err
	}
	// OD/KSI game state is a pure function of the TEAM list, so only re-propagate
	// when teams actually changed — a player-only change leaves it identical.
	if teamsChanged {
		updates, err := roster.PropagateRosterTx(ctx, tx, festID, teams, nil)
		if err != nil {
			return out, err
		}
		out.Updates = updates
		for _, u := range updates {
			switch u.GameType {
			case games.OD:
				out.odGames++
			case games.KSI:
				out.ksiGames++
			}
		}
	}
	// Refresh EK override game rosters. With fest_players ids stable the surviving
	// overrides still point at the right rows (orphaned ones cascaded away with
	// their deleted player); re-resolving them re-points any moved source team and
	// re-materializes the affected EK game_team_players caches.
	current, err := overrides.LoadRatingPlayerTeamOverrides(ctx, tx, festID)
	if err != nil {
		return out, err
	}
	if out.EKGameIDs, err = overrides.RestoreRatingPlayerTeamOverridesTx(ctx, tx, festID, current); err != nil {
		return out, err
	}
	return out, nil
}

// rewriteFromHandTx writes the fest roster anew from its rows and edits: the
// site's last roster, worked back from them, with the edits merged over it.
// A hand edit loads the roster before it writes anything, records its edit
// and calls this with what it loaded: the flat games are rewritten when the
// teams differ from that, and comparing with the rows the edit already wrote
// would find nothing changed.
func rewriteFromHandTx(ctx context.Context, tx *sql.Tx, festID int64, before roster.HandState) (RosterWrite, error) {
	if err := ForgetRosterSnapshotsTx(ctx, tx, festID); err != nil {
		return RosterWrite{}, err
	}
	state, err := roster.LoadHandState(ctx, tx, festID)
	if err != nil {
		return RosterWrite{}, err
	}
	existing, err := loadFestExistingTeams(ctx, tx, festID)
	if err != nil {
		return RosterWrite{}, err
	}
	desired := roster.SortedFestRosterImportTeams(roster.MergeHand(roster.Baseline(state), state, nil).Desired)
	assignFestNumbersForImport(desired, byRatingID(existing), maxTeamNumber(existing))
	current := roster.SortedFestRosterImportTeams(stripLocal(activeRoster(before)))
	return writeRosterTx(ctx, tx, festID, desired, byRatingID(existing), nil, !teamLevelEqual(current, stripLocal(desired)))
}

// TeamInput is a team as the host types it: a name, a city, and its people in
// order. A person with a rating id is that rating player; one without is a
// person the site does not know, named as typed.
type TeamInput struct {
	// RatingID, for a new team, is the rating site's team it is: its name
	// may still be a one-off for this fest.
	RatingID int64
	Name     string
	City     string
	Players  []roster.FestRosterImportPlayer
}

func (in TeamInput) normalized() (TeamInput, error) {
	s := dopestrings.Default
	in.Name = strings.TrimSpace(in.Name)
	in.City = strings.TrimSpace(in.City)
	if in.Name == "" {
		return in, corei18n.User(s.Imports.HandRoster.NameMissing())
	}
	seen := map[string]bool{}
	var players []roster.FestRosterImportPlayer
	for _, p := range in.Players {
		p.FirstName, p.LastName = strings.TrimSpace(p.FirstName), strings.TrimSpace(p.LastName)
		if p.FirstName == "" && p.LastName == "" {
			continue
		}
		key := roster.PlayerKey(p)
		if seen[key] {
			return in, corei18n.User(s.Imports.HandRoster.PlayerTwice(store.JoinPlayerName(p.FirstName, p.LastName)))
		}
		seen[key] = true
		players = append(players, p)
	}
	in.Players = players
	return in, nil
}

// checkNameFree refuses a name and city another active team already goes by:
// the pages and the numbers match teams by them.
func checkNameFree(ctx context.Context, tx *sql.Tx, festID, teamID int64, name, city string) error {
	var clash int
	if err := tx.QueryRowContext(ctx, `
select count(*) from fest_teams
where fest_id = ? and deleted = 0 and id != ? and lower(name) = lower(?) and lower(coalesce(city, '')) = lower(?)`,
		festID, teamID, name, city).Scan(&clash); err != nil {
		return err
	}
	if clash > 0 {
		return corei18n.User(dopestrings.Default.Imports.HandRoster.NameTaken(name))
	}
	return nil
}

// CreateHandTeamTx adds a team the host made (ADR-0024): no rating id, the
// next free number, its people as the host's edits. Returns the team's id.
func CreateHandTeamTx(ctx context.Context, tx *sql.Tx, festID int64, in TeamInput) (int64, RosterWrite, error) {
	in, err := in.normalized()
	if err != nil {
		return 0, RosterWrite{}, err
	}
	if err := checkNameFree(ctx, tx, festID, 0, in.Name, in.City); err != nil {
		return 0, RosterWrite{}, err
	}
	before, err := roster.LoadHandState(ctx, tx, festID)
	if err != nil {
		return 0, RosterWrite{}, err
	}
	var number int64
	var position float64
	if err := tx.QueryRowContext(ctx, `
select coalesce(max(number), 0) + 1, coalesce(max(position), 0) + 1 from fest_teams where fest_id = ?`, festID).Scan(&number, &position); err != nil {
		return 0, RosterWrite{}, err
	}
	if in.RatingID > 0 {
		var already int
		if err := tx.QueryRowContext(ctx, `select count(*) from fest_teams where fest_id = ? and rating_id = ? and deleted = 0`,
			festID, in.RatingID).Scan(&already); err != nil {
			return 0, RosterWrite{}, err
		}
		if already > 0 {
			return 0, RosterWrite{}, corei18n.User(dopestrings.Default.Imports.HandRoster.RatingTeamTaken())
		}
	}
	teamID, err := store.InsertReturningID(ctx, tx, `
insert into fest_teams(fest_id, rating_id, name, city, position, number, deleted, hand) values(?, ?, ?, ?, ?, ?, 0, 1)`,
		festID, util.NullableInt64(in.RatingID), in.Name, in.City, position, number)
	if err != nil {
		return 0, RosterWrite{}, err
	}
	if err := saveTeamPlayersTx(ctx, tx, festID, teamID, nil, in.Players); err != nil {
		return 0, RosterWrite{}, err
	}
	written, err := rewriteFromHandTx(ctx, tx, festID, before)
	return teamID, written, err
}

// SaveHandTeamTx sets a team's name, city and people as the host typed them.
// A team the host made takes them outright. A rating team keeps the host's
// name and city apart from the site's, and each person added or taken off is
// an edit an import applies again. A person added who sits on another team
// moves: they are taken off that one.
func SaveHandTeamTx(ctx context.Context, tx *sql.Tx, festID, teamID int64, in TeamInput) (RosterWrite, error) {
	in, err := in.normalized()
	if err != nil {
		return RosterWrite{}, err
	}
	state, err := roster.LoadHandState(ctx, tx, festID)
	if err != nil {
		return RosterWrite{}, err
	}
	var row *roster.HandTeam
	for i := range state.Teams {
		if state.Teams[i].ID == teamID && !state.Teams[i].Deleted {
			row = &state.Teams[i]
		}
	}
	if row == nil {
		return RosterWrite{}, corei18n.User(dopestrings.Default.Imports.HandRoster.TeamUnknown())
	}
	if err := checkNameFree(ctx, tx, festID, teamID, in.Name, in.City); err != nil {
		return RosterWrite{}, err
	}
	switch {
	case row.Hand:
		if _, err := tx.ExecContext(ctx, `update fest_teams set name = ?, city = ? where id = ?`, in.Name, in.City, teamID); err != nil {
			return RosterWrite{}, err
		}
	default:
		if in.Name != row.Name {
			if _, err := tx.ExecContext(ctx, `update fest_teams set hand_name = ? where id = ?`, in.Name, teamID); err != nil {
				return RosterWrite{}, err
			}
		}
		if in.City != row.City {
			if _, err := tx.ExecContext(ctx, `update fest_teams set hand_city = ? where id = ?`, in.City, teamID); err != nil {
				return RosterWrite{}, err
			}
		}
	}
	// A person added here leaves whatever other team they are on.
	elsewhere := map[string][]int64{}
	for _, team := range state.Teams {
		if team.Deleted || team.ID == teamID {
			continue
		}
		for _, p := range team.Players {
			elsewhere[roster.PlayerKey(p)] = append(elsewhere[roster.PlayerKey(p)], team.ID)
		}
	}
	now := util.UtcNow()
	current := map[string]bool{}
	for _, p := range row.Players {
		current[roster.PlayerKey(p)] = true
	}
	for _, p := range in.Players {
		if current[roster.PlayerKey(p)] {
			continue
		}
		for _, other := range elsewhere[roster.PlayerKey(p)] {
			if err := roster.SaveHandEditTx(ctx, tx, festID, roster.HandEdit{TeamID: other, Player: p, Remove: true}, now); err != nil {
				return RosterWrite{}, err
			}
		}
	}
	if err := saveTeamPlayersTx(ctx, tx, festID, teamID, row.Players, in.Players); err != nil {
		return RosterWrite{}, err
	}
	return rewriteFromHandTx(ctx, tx, festID, state)
}

// saveTeamPlayersTx records the edits that take a team from its people now to
// the people wanted.
func saveTeamPlayersTx(ctx context.Context, tx *sql.Tx, festID, teamID int64, now, want []roster.FestRosterImportPlayer) error {
	at := util.UtcNow()
	wanted := map[string]bool{}
	for _, p := range want {
		wanted[roster.PlayerKey(p)] = true
	}
	had := map[string]bool{}
	for _, p := range now {
		had[roster.PlayerKey(p)] = true
		if !wanted[roster.PlayerKey(p)] {
			if err := roster.SaveHandEditTx(ctx, tx, festID, roster.HandEdit{TeamID: teamID, Player: p, Remove: true}, at); err != nil {
				return err
			}
		}
	}
	for _, p := range want {
		if !had[roster.PlayerKey(p)] {
			if err := roster.SaveHandEditTx(ctx, tx, festID, roster.HandEdit{TeamID: teamID, Player: p}, at); err != nil {
				return err
			}
		}
	}
	return nil
}

// RemoveTeamTx takes a team off the fest roster. A team with results stays:
// they are scored under its number. A rating team the host took off stays off
// through later imports.
func RemoveTeamTx(ctx context.Context, tx *sql.Tx, festID, teamID int64) (RosterWrite, error) {
	s := dopestrings.Default
	var number int64
	var name string
	err := tx.QueryRowContext(ctx, `select coalesce(number, 0), name from fest_teams where id = ? and fest_id = ? and deleted = 0`,
		teamID, festID).Scan(&number, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return RosterWrite{}, corei18n.User(s.Imports.HandRoster.TeamUnknown())
	}
	if err != nil {
		return RosterWrite{}, err
	}
	scored, err := roster.ScoredNumbers(ctx, tx, festID)
	if err != nil {
		return RosterWrite{}, err
	}
	if titles := scored[number]; number > 0 && len(titles) > 0 {
		return RosterWrite{}, corei18n.User(s.Imports.HandRoster.TeamScored(name, strings.Join(titles, ", ")))
	}
	before, err := roster.LoadHandState(ctx, tx, festID)
	if err != nil {
		return RosterWrite{}, err
	}
	if _, err := tx.ExecContext(ctx, `update fest_teams set deleted = 1, hand_removed = 1 where id = ?`, teamID); err != nil {
		return RosterWrite{}, err
	}
	if _, err := tx.ExecContext(ctx, `delete from fest_team_players where team_id = ?`, teamID); err != nil {
		return RosterWrite{}, err
	}
	return rewriteFromHandTx(ctx, tx, festID, before)
}

// saveRosterSnapshotTx keeps the roster as it was before an import, with the
// host's edits, for the undo on the import page. The last ten are kept.
func saveRosterSnapshotTx(ctx context.Context, tx *sql.Tx, festID int64, reason string, state roster.HandState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
insert into fest_roster_snapshots(fest_id, reason, roster_json, created_at) values(?, ?, ?, ?)`,
		festID, reason, string(raw), util.UtcNow()); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
delete from fest_roster_snapshots where fest_id = ? and id not in (
  select id from fest_roster_snapshots where fest_id = ? order by id desc limit 10)`, festID, festID)
	return err
}

// ForgetRosterSnapshotsTx drops the saved rosters. A hand edit calls it: an
// undo puts back the roster from before an import, and that roster does not
// have the edits made since, so after an edit there is no import to undo.
func ForgetRosterSnapshotsTx(ctx context.Context, tx *sql.Tx, festID int64) error {
	_, err := tx.ExecContext(ctx, `delete from fest_roster_snapshots where fest_id = ?`, festID)
	return err
}

// LastRosterSnapshot is when the roster was last saved before an import, ""
// when there is nothing to undo.
func LastRosterSnapshot(ctx context.Context, q store.Queryer, festID int64) (string, error) {
	var at string
	err := q.QueryRowContext(ctx, `select created_at from fest_roster_snapshots where fest_id = ? order by id desc limit 1`, festID).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return at, err
}

// UndoRosterImportTx puts the fest roster back as it was before the last
// import, the host's edits with it, through the same writer an import uses.
// The snapshot is used up, so undoing again goes one import further back.
func UndoRosterImportTx(ctx context.Context, tx *sql.Tx, festID int64) (RosterWrite, error) {
	var id int64
	var raw string
	err := tx.QueryRowContext(ctx, `select id, roster_json from fest_roster_snapshots where fest_id = ? order by id desc limit 1`, festID).Scan(&id, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return RosterWrite{}, corei18n.User(dopestrings.Default.Imports.HandRoster.NothingToUndo())
	}
	if err != nil {
		return RosterWrite{}, err
	}
	var snap roster.HandState
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return RosterWrite{}, err
	}
	before, err := roster.LoadHandState(ctx, tx, festID)
	if err != nil {
		return RosterWrite{}, err
	}
	inSnapshot := map[int64]bool{}
	for _, t := range snap.Teams {
		inSnapshot[t.ID] = true
		if _, err := tx.ExecContext(ctx, `
update fest_teams set rating_id = ?, hand = ?, hand_name = ?, hand_city = ?, hand_flags = ?, hand_removed = ?, deleted = ?
where id = ? and fest_id = ?`, util.NullableInt64(t.RatingID), t.Hand, t.HandName, t.HandCity, t.HandFlags, t.HandRemoved, t.Deleted, t.ID, festID); err != nil {
			return RosterWrite{}, err
		}
	}
	existing, err := loadFestExistingTeams(ctx, tx, festID)
	if err != nil {
		return RosterWrite{}, err
	}
	for _, row := range existing {
		if !inSnapshot[row.ID] && !row.Deleted {
			if _, err := tx.ExecContext(ctx, `update fest_teams set deleted = 1 where id = ?`, row.ID); err != nil {
				return RosterWrite{}, err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `delete from fest_roster_edits where fest_id = ?`, festID); err != nil {
		return RosterWrite{}, err
	}
	at := util.UtcNow()
	for _, edit := range snap.Edits {
		if err := roster.SaveHandEditTx(ctx, tx, festID, edit, at); err != nil {
			return RosterWrite{}, err
		}
	}
	desired := roster.SortedFestRosterImportTeams(activeRoster(snap))
	current := roster.SortedFestRosterImportTeams(stripLocal(activeRoster(before)))
	existing, err = loadFestExistingTeams(ctx, tx, festID)
	if err != nil {
		return RosterWrite{}, err
	}
	written, err := writeRosterTx(ctx, tx, festID, desired, byRatingID(existing), nil, !teamLevelEqual(current, stripLocal(desired)))
	if err != nil {
		return RosterWrite{}, err
	}
	_, err = tx.ExecContext(ctx, `delete from fest_roster_snapshots where id = ?`, id)
	return written, err
}

// activeRoster is a state's teams on the roster now, each on its own row.
func activeRoster(state roster.HandState) []roster.FestRosterImportTeam {
	var out []roster.FestRosterImportTeam
	for _, t := range state.Teams {
		if t.Deleted {
			continue
		}
		out = append(out, roster.FestRosterImportTeam{
			LocalID: t.ID, RatingID: t.RatingID, Name: t.Name, City: t.City, Country: t.Country,
			Number: t.Number, Flags: t.Flags, Players: t.Players,
		})
	}
	return out
}

// stripLocal drops the row ids, for comparing rosters by what they say.
func stripLocal(teams []roster.FestRosterImportTeam) []roster.FestRosterImportTeam {
	out := make([]roster.FestRosterImportTeam, len(teams))
	for i, t := range teams {
		t.LocalID = 0
		out[i] = t
	}
	return out
}

// removeOf turns the adds a conflict answer drops into the removes that
// cancel them.
func removeOf(edits []roster.HandEdit) []roster.HandEdit {
	out := make([]roster.HandEdit, len(edits))
	for i, e := range edits {
		e.Remove = true
		out[i] = e
	}
	return out
}

// planRoster is what taking the fest from current to desired does.
func planRoster(current, desired []roster.FestRosterImportTeam, merged roster.MergeResult) ImportPlan {
	plan := ImportPlan{Kept: merged.Kept, Conflicts: merged.Conflicts}
	key := func(t roster.FestRosterImportTeam) string {
		if t.LocalID > 0 {
			return "id:" + strconv.FormatInt(t.LocalID, 10)
		}
		return "rating:" + strconv.FormatInt(t.RatingID, 10)
	}
	label := func(t roster.FestRosterImportTeam) string {
		if t.City == "" {
			return t.Name
		}
		return t.Name + " (" + t.City + ")"
	}
	now := map[string]roster.FestRosterImportTeam{}
	for _, t := range current {
		now[key(t)] = t
	}
	next := map[string]bool{}
	for _, t := range desired {
		next[key(t)] = true
		was, ok := now[key(t)]
		if !ok {
			plan.AddedTeams = append(plan.AddedTeams, label(t))
			continue
		}
		if label(was) != label(t) {
			plan.Renamed = append(plan.Renamed, TeamRename{From: label(was), To: label(t)})
		}
		had := map[string]bool{}
		for _, p := range was.Players {
			had[roster.PlayerKey(p)] = true
		}
		wants := map[string]bool{}
		change := TeamPlayers{Team: label(t)}
		for _, p := range t.Players {
			wants[roster.PlayerKey(p)] = true
			if !had[roster.PlayerKey(p)] {
				change.Added = append(change.Added, store.JoinPlayerName(p.FirstName, p.LastName))
			}
		}
		for _, p := range was.Players {
			if !wants[roster.PlayerKey(p)] {
				change.Removed = append(change.Removed, store.JoinPlayerName(p.FirstName, p.LastName))
			}
		}
		if len(change.Added)+len(change.Removed) > 0 {
			plan.Players = append(plan.Players, change)
		}
	}
	for _, t := range current {
		if !next[key(t)] {
			plan.DroppedTeams = append(plan.DroppedTeams, label(t))
		}
	}
	sort.Strings(plan.AddedTeams)
	sort.Strings(plan.DroppedTeams)
	sort.SliceStable(plan.Players, func(i, j int) bool { return util.CompareAlpha(plan.Players[i].Team, plan.Players[j].Team) < 0 })
	return plan
}
