package imports

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dope/dope/domain/core"
	"dope/dope/domain/games"
	"dope/dope/domain/overrides"
	"dope/dope/domain/roster"
	"dope/dope/domain/towns"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"
	corei18n "pecheny.me/dopecore/i18nstrings"
)

const ratingResultsURL = "https://api.rating.chgk.net/tournaments/%d/results.json?includeTeamMembers=1&includeTeamFlags=1"

type RatingRosterImportResult struct {
	TeamCount    int
	PlayerCount  int
	ODGameCount  int
	KSIGameCount int
	// Unchanged is set when the incoming roster matched the fest's current roster
	// exactly, so the import short-circuited to a no-op (no writes, no game-state
	// propagation, no broadcasts). TeamCount/PlayerCount still report the roster
	// size; the game counts stay zero because nothing was rewritten.
	Unchanged bool
}

type ratingFestResult struct {
	Team        ratingTeam         `json:"team"`
	Current     ratingTeam         `json:"current"`
	TeamMembers []ratingTeamMember `json:"teamMembers"`
	// Flags are the team's Divisions as the rating site marks them: an id, a
	// full name and the short name everything shows. Every one is kept — the app
	// does not judge which Flags deserve a Division (ADR-0020).
	Flags []ratingTeamFlag `json:"flags"`
}

type ratingTeamFlag struct {
	ID        int64  `json:"id"`
	FullName  string `json:"fullName"`
	ShortName string `json:"shortName"`
}

type ratingTeam struct {
	ID   int64       `json:"id"`
	Name string      `json:"name"`
	Town *ratingTown `json:"town"`
}

type ratingTown struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type ratingTeamMember struct {
	Player ratingPlayer `json:"player"`
}

type ratingPlayer struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Surname string `json:"surname"`
}

func FetchAndImportRatingRoster(eng *core.Engine, ctx context.Context, festID, ratingID int64, choice RosterChoice) (RatingRosterImportResult, error) {
	teams, err := fetchRatingFestRoster(ctx, ratingID)
	if err != nil {
		return RatingRosterImportResult{}, err
	}
	resolveTeamCountries(ctx, eng, teams)
	return ImportFestRoster(eng, ctx, festID, ratingID, teams, choice)
}

// DroppedTeam is a team the fest has, that the incoming roster no longer
// lists, and that already carries results. Games names the games those results
// are in, so the host is told what would be lost.
type DroppedTeam struct {
	TeamID   int64
	RatingID int64
	Number   int64
	Name     string
	City     string
	Games    []string
}

// AddedTeam is a team the incoming roster brings that the fest does not have
// yet — one of these is what a DroppedTeam may really be, under a new id.
type AddedTeam struct {
	RatingID int64
	Name     string
	City     string
}

// RosterConflict is what an import stops on instead of silently throwing
// results away. A team whose id changed on rating.chgk.info looks like one
// team leaving and another arriving, and the import cannot tell that apart
// from a team that really withdrew — so it asks, listing both sides.
type RosterConflict struct {
	Dropped []DroppedTeam
	Added   []AddedTeam
}

func (c *RosterConflict) Error() string {
	return dopestrings.Default.Imports.Rating.ConflictError(strconv.Itoa(len(c.Dropped)))
}

// RosterChoice is the host's answer to a RosterConflict. Merge says a team the
// fest already has (by fest team id) is the same team as an incoming one (by
// the rating id the site now gives it); Drop says a team may lose its results.
// The zero value answers nothing, so an import that finds a conflict stops.
type RosterChoice struct {
	Merge map[int64]int64
	Drop  map[int64]bool
}

// resolveTeamCountries fills in the country each team's town is in, before the
// import opens its write transaction: buff answers for every town it has
// mirrored, and the towns it has not are asked about over HTTP, which has no
// business inside a transaction.
func resolveTeamCountries(ctx context.Context, eng *core.Engine, teams []roster.FestRosterImportTeam) {
	townIDs := make([]int64, 0, len(teams))
	for _, team := range teams {
		townIDs = append(townIDs, team.TownID)
	}
	countries := towns.NewResolver(eng.Buff).Countries(ctx, townIDs)
	for i := range teams {
		teams[i].Country = countries[teams[i].TownID]
	}
}

func fetchRatingFestRoster(ctx context.Context, ratingID int64) ([]roster.FestRosterImportTeam, error) {
	if ratingID <= 0 {
		return nil, errors.New("rating fest id must be positive")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(ratingResultsURL, ratingID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, corei18n.User(dopestrings.Default.Imports.Rating.FetchFailed(err.Error()))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		detail := strings.TrimSpace(string(body))
		if detail == "" {
			detail = resp.Status
		}
		return nil, corei18n.User(dopestrings.Default.Imports.Rating.ApiError(detail))
	}

	var results []ratingFestResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, corei18n.User(dopestrings.Default.Imports.Rating.DecodeFailed(err.Error()))
	}
	return ratingResultsToFestRoster(results)
}

func ratingResultsToFestRoster(results []ratingFestResult) ([]roster.FestRosterImportTeam, error) {
	teams := make([]roster.FestRosterImportTeam, 0, len(results))
	for index, result := range results {
		name := strings.TrimSpace(result.Current.Name)
		if name == "" {
			name = strings.TrimSpace(result.Team.Name)
		}
		if name == "" {
			return nil, fmt.Errorf("team %d has no name", index+1)
		}
		town := result.Current.Town
		if ratingTownName(town) == "" {
			town = result.Team.Town
		}
		team := roster.FestRosterImportTeam{
			RatingID: result.Team.ID,
			Name:     name,
			City:     ratingTownName(town),
			TownID:   ratingTownID(town),
			Players:  make([]roster.FestRosterImportPlayer, 0, len(result.TeamMembers)),
		}
		for memberIndex, member := range result.TeamMembers {
			firstName := strings.TrimSpace(member.Player.Name)
			lastName := strings.TrimSpace(member.Player.Surname)
			if firstName == "" && lastName == "" {
				return nil, fmt.Errorf("team %q player %d has no name", name, memberIndex+1)
			}
			team.Players = append(team.Players, roster.FestRosterImportPlayer{
				RatingID:  member.Player.ID,
				FirstName: firstName,
				LastName:  lastName,
			})
		}
		for _, flag := range result.Flags {
			team.Flags = append(team.Flags, roster.FestRosterFlag{
				RatingID: flag.ID,
				Short:    strings.TrimSpace(flag.ShortName),
				Full:     strings.TrimSpace(flag.FullName),
			})
		}
		team.Flags = roster.NormalizeFlags(team.Flags)
		if len(team.Players) > 9 {
			return nil, corei18n.User(dopestrings.Default.Imports.Rating.SquadTooBig(name))
		}
		teams = append(teams, team)
	}
	return roster.SortedFestRosterImportTeams(teams), nil
}

func ratingTownName(town *ratingTown) string {
	if town == nil {
		return ""
	}
	return strings.TrimSpace(town.Name)
}

func ratingTownID(town *ratingTown) int64 {
	if town == nil {
		return 0
	}
	return town.ID
}

func ImportFestRoster(eng *core.Engine, ctx context.Context, festID, ratingID int64, teams []roster.FestRosterImportTeam, choice RosterChoice) (RatingRosterImportResult, error) {
	if eng.DB == nil {
		return RatingRosterImportResult{}, errors.New("sqlite is not enabled")
	}
	if festID <= 0 {
		return RatingRosterImportResult{}, errors.New("bad fest id")
	}
	if len(teams) == 0 {
		return RatingRosterImportResult{}, corei18n.User(dopestrings.Default.Imports.Rating.NoTeams())
	}
	teams = roster.SortedFestRosterImportTeams(teams)

	// Acquire the pooled connection OFF the global write lock. The import is a
	// bulk op so it keeps the request context (no festwrite.WriteTxTimeout cap — a large
	// rebuild may legitimately run several seconds), but the pool wait must never
	// pin eng.Mu (see the 2026-06-13 freeze).
	conn, err := eng.AcquireWriteConn(ctx, "rating-import")
	if err != nil {
		return RatingRosterImportResult{}, err
	}
	defer conn.Close()

	var updates []roster.GameStateBroadcast
	var ekOverrideGameIDs []int64
	var revision int64
	result, err := func() (RatingRosterImportResult, error) {
		defer eng.LockWrite("rating-import")()

		var exists int
		if err := conn.QueryRowContext(ctx, `select count(*) from fests where id = ?`, festID).Scan(&exists); err != nil {
			return RatingRosterImportResult{}, err
		}
		if exists == 0 {
			return RatingRosterImportResult{}, sql.ErrNoRows
		}

		existingTeams, err := loadFestExistingTeams(ctx, conn, festID)
		if err != nil {
			return RatingRosterImportResult{}, err
		}
		// A team whose id changed on the site is one row the fest already has,
		// under an id the incoming roster no longer carries. Merging re-keys that
		// row onto the new id, here and (below) in the database, so everything
		// underneath — the row, its number, and every result scored under that
		// number — stays where it is and only the id changes.
		if err := validateRosterMerges(existingTeams, teams, choice.Merge); err != nil {
			return RatingRosterImportResult{}, err
		}
		existingTeams = applyRosterMerges(existingTeams, choice.Merge)
		existingByRating := byRatingID(existingTeams)

		conflict, err := rosterConflict(ctx, conn, festID, teams, existingTeams, choice)
		if err != nil {
			return RatingRosterImportResult{}, err
		}
		if conflict != nil {
			return RatingRosterImportResult{}, conflict
		}

		assignFestNumbersForImport(teams, existingByRating, maxTeamNumber(existingTeams))

		// Fast path: if the incoming roster is identical to the fest's current
		// active roster (same teams, numbers, and players in canonical order), the
		// rebuild below would rewrite every row to its current value and re-derive
		// identical game state — all no-ops. Skip the whole write tx, propagation,
		// and broadcasts, so a "refresh" that changed nothing is near-instant and
		// adds zero churn during a live tournament. These reads run on conn outside
		// any tx (we hold eng.Mu, so no writer can race them).
		current, err := loadFestActiveRoster(ctx, conn, festID)
		if err != nil {
			return RatingRosterImportResult{}, err
		}
		sortedCurrent := roster.SortedFestRosterImportTeams(current)
		if festRostersEqual(sortedCurrent, teams) {
			return RatingRosterImportResult{
				TeamCount:   len(teams),
				PlayerCount: distinctPlayerCount(teams),
				Unchanged:   true,
			}, nil
		}

		tx, err := eng.BeginWriteTxConn(ctx, conn)
		if err != nil {
			return RatingRosterImportResult{}, err
		}
		defer tx.Rollback()

		// A rating.chgk.info roster import is a bulk machine sync; suppress per-row
		// audit capture (its churn has no incremental-undo value and is recorded as
		// the single 'rating:roster-import' event below). Manual host roster edits
		// run in their own un-suppressed tx and stay audited.
		if err := festwrite.SuppressAuditTx(ctx, tx); err != nil {
			return RatingRosterImportResult{}, err
		}

		// Bring the canonical roster (fest_teams/fest_players/fest_team_players) to
		// match the incoming teams by writing ONLY what changed. Crucially this keeps
		// fest_players ids stable for players that stay, so game_player_team_overrides
		// (FK fest_players.id ON DELETE CASCADE) survive without a preserve/restore
		// dance; a player who left the roster is deleted and its override cascades
		// away. See applyFestRosterDiffTx.
		if err := applyFestRosterDiffTx(ctx, tx, festID, teams, existingByRating, choice.Merge); err != nil {
			return RatingRosterImportResult{}, err
		}
		playerCount := distinctPlayerCount(teams)

		// OD/KSI game state is a pure function of the TEAM list, so only re-propagate
		// when teams actually changed — a player-only change leaves it identical.
		if !teamLevelEqual(sortedCurrent, teams) {
			if updates, err = roster.PropagateRosterTx(ctx, tx, festID, teams, nil); err != nil {
				return RatingRosterImportResult{}, err
			}
		}
		odGames, ksiGames := 0, 0
		for _, u := range updates {
			switch u.GameType {
			case games.OD:
				odGames++
			case games.KSI:
				ksiGames++
			}
		}

		// Refresh EK override game rosters. With fest_players ids stable the surviving
		// overrides still point at the right rows (orphaned ones cascaded away with
		// their deleted player); re-resolving them re-points any moved source team and
		// re-materializes the affected EK game_team_players caches.
		currentOverrides, err := overrides.LoadRatingPlayerTeamOverrides(ctx, tx, festID)
		if err != nil {
			return RatingRosterImportResult{}, err
		}
		ekOverrideGameIDs, err = overrides.RestoreRatingPlayerTeamOverridesTx(ctx, tx, festID, currentOverrides)
		if err != nil {
			return RatingRosterImportResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `update fests set rating_id = ?, updated_at = ? where id = ?`, ratingID, util.UtcNow(), festID); err != nil {
			return RatingRosterImportResult{}, err
		}
		revision, err = festwrite.BumpFestRevisionTx(ctx, tx, festID, "rating:roster-import", util.MustJSON(map[string]any{
			"ratingID": ratingID,
			"teams":    len(teams),
			"players":  playerCount,
			"odGames":  odGames,
			"ksiGames": ksiGames,
		}))
		if err != nil {
			return RatingRosterImportResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return RatingRosterImportResult{}, err
		}

		return RatingRosterImportResult{
			TeamCount:    len(teams),
			PlayerCount:  playerCount,
			ODGameCount:  odGames,
			KSIGameCount: ksiGames,
		}, nil
	}()
	if err != nil {
		return RatingRosterImportResult{}, err
	}

	for _, update := range updates {
		eng.BroadcastState(festID, core.GameStateScope(update.GameID), revision, update.StateJSON)
	}
	for _, gameID := range ekOverrideGameIDs {
		eng.BroadcastState(festID, fmt.Sprintf("game-roster:%d", gameID), revision, []byte(`{}`))
	}
	return result, nil
}

type existingFestTeam struct {
	ID       int64
	RatingID int64
	Number   int64
	Name     string
	City     string
	Deleted  bool
}

// loadFestExistingTeams returns every fest_team row of this fest, including the
// soft-deleted ones, so that a previously archived number can be restored when
// the team is re-added.
func loadFestExistingTeams(ctx context.Context, q store.Queryer, festID int64) ([]existingFestTeam, error) {
	return store.CollectRows(ctx, q, `
select id, coalesce(rating_id, 0), coalesce(number, 0), name, coalesce(city, ''), deleted
from fest_teams
where fest_id = ?
order by position, id`, []any{festID}, func(rows *sql.Rows) (existingFestTeam, error) {
		var team existingFestTeam
		return team, rows.Scan(&team.ID, &team.RatingID, &team.Number, &team.Name, &team.City, &team.Deleted)
	})
}

// byRatingID keys the rows that carry a rating id by that id — the only handle
// the import can match an incoming team to an existing row with.
func byRatingID(rows []existingFestTeam) map[int64]existingFestTeam {
	out := make(map[int64]existingFestTeam, len(rows))
	for _, row := range rows {
		if row.RatingID > 0 {
			out[row.RatingID] = row
		}
	}
	return out
}

// maxTeamNumber is the largest number ever assigned in this fest, soft-deleted
// rows included. New teams introduced by a re-sync always receive numbers
// strictly greater than this, so already-printed answer sheets keep referring
// to the right team.
func maxTeamNumber(rows []existingFestTeam) int64 {
	var max int64
	for _, row := range rows {
		if row.Number > max {
			max = row.Number
		}
	}
	return max
}

// applyRosterMerges re-keys the rows the host merged onto the rating id the
// site now gives them, so every rule below sees them as teams that stayed.
func applyRosterMerges(rows []existingFestTeam, merge map[int64]int64) []existingFestTeam {
	if len(merge) == 0 {
		return rows
	}
	out := append([]existingFestTeam(nil), rows...)
	for i := range out {
		if ratingID, ok := merge[out[i].ID]; ok && ratingID > 0 {
			out[i].RatingID = ratingID
		}
	}
	return out
}

// validateRosterMerges rejects a merge the import could not honour: onto an id
// the incoming roster does not carry, or onto one another team of this fest
// already holds. Both mean the form the host answered has gone stale, and
// going ahead would drop exactly the results the question was asked about.
func validateRosterMerges(rows []existingFestTeam, teams []roster.FestRosterImportTeam, merge map[int64]int64) error {
	if len(merge) == 0 {
		return nil
	}
	incoming := make(map[int64]struct{}, len(teams))
	for _, team := range teams {
		if team.RatingID > 0 {
			incoming[team.RatingID] = struct{}{}
		}
	}
	taken := make(map[int64]bool, len(rows))
	for _, row := range rows {
		if _, merged := merge[row.ID]; merged {
			continue
		}
		if row.RatingID > 0 {
			taken[row.RatingID] = true
		}
	}
	seen := make(map[int64]bool, len(merge))
	for _, ratingID := range merge {
		if _, ok := incoming[ratingID]; !ok || taken[ratingID] || seen[ratingID] {
			return corei18n.User(dopestrings.Default.Imports.Rating.MergeStale())
		}
		seen[ratingID] = true
	}
	return nil
}

// rosterConflict reports the teams this import would take off the roster while
// they still carry results, or nil when there is nothing to ask about. A team
// the host has already merged is not leaving, and one the host has agreed to
// drop has been asked about already.
func rosterConflict(ctx context.Context, q store.Queryer, festID int64, teams []roster.FestRosterImportTeam, rows []existingFestTeam, choice RosterChoice) (*RosterConflict, error) {
	incoming := make(map[int64]struct{}, len(teams))
	for _, team := range teams {
		if team.RatingID > 0 {
			incoming[team.RatingID] = struct{}{}
		}
	}
	var leaving []existingFestTeam
	for _, row := range rows {
		if row.Deleted || choice.Drop[row.ID] {
			continue
		}
		if _, stays := incoming[row.RatingID]; stays && row.RatingID > 0 {
			continue
		}
		leaving = append(leaving, row)
	}
	if len(leaving) == 0 {
		return nil, nil
	}
	scored, err := roster.ScoredNumbers(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	var dropped []DroppedTeam
	for _, row := range leaving {
		games := scored[row.Number]
		if row.Number <= 0 || len(games) == 0 {
			continue
		}
		dropped = append(dropped, DroppedTeam{
			TeamID: row.ID, RatingID: row.RatingID, Number: row.Number,
			Name: row.Name, City: row.City, Games: games,
		})
	}
	if len(dropped) == 0 {
		return nil, nil
	}
	var added []AddedTeam
	existing := byRatingID(rows)
	for _, team := range teams {
		if team.RatingID <= 0 {
			continue
		}
		if _, have := existing[team.RatingID]; have {
			continue
		}
		added = append(added, AddedTeam{RatingID: team.RatingID, Name: team.Name, City: team.City})
	}
	return &RosterConflict{Dropped: dropped, Added: added}, nil
}

// loadFestActiveRoster loads the fest's current ACTIVE (non-deleted) teams and
// their players in the same roster.FestRosterImportTeam shape as an incoming rating
// roster, so the two can be diffed to detect a no-op re-import. Soft-deleted
// teams are excluded: a re-import that re-adds one would flip its deleted flag,
// which is a real change and must not be mistaken for "unchanged".
func loadFestActiveRoster(ctx context.Context, q store.Queryer, festID int64) ([]roster.FestRosterImportTeam, error) {
	type teamRow struct {
		id       int64
		ratingID int64
		name     string
		city     string
		number   int64
	}
	teamRows, err := store.CollectRows(ctx, q, `
select id, coalesce(rating_id, 0), name, coalesce(city, ''), coalesce(number, 0)
from fest_teams
where fest_id = ? and deleted = 0
order by position, id`, []any{festID}, func(rows *sql.Rows) (teamRow, error) {
		var t teamRow
		return t, rows.Scan(&t.id, &t.ratingID, &t.name, &t.city, &t.number)
	})
	if err != nil {
		return nil, err
	}
	flagsByTeam, err := roster.LoadFestTeamFlags(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	out := make([]roster.FestRosterImportTeam, 0, len(teamRows))
	for _, t := range teamRows {
		players, err := store.CollectRows(ctx, q, `
select coalesce(p.rating_id, 0), p.first_name, p.last_name
from fest_team_players ftp
join fest_players p on p.id = ftp.player_id
where ftp.team_id = ?
order by ftp.roster_order, p.id`, []any{t.id}, func(rows *sql.Rows) (roster.FestRosterImportPlayer, error) {
			var p roster.FestRosterImportPlayer
			return p, rows.Scan(&p.RatingID, &p.FirstName, &p.LastName)
		})
		if err != nil {
			return nil, err
		}
		out = append(out, roster.FestRosterImportTeam{
			RatingID: t.ratingID,
			Name:     t.name,
			City:     t.city,
			Number:   t.number,
			Players:  players,
			Flags:    flagsByTeam[t.id],
		})
	}
	return out, nil
}

// festRostersEqual reports whether two rosters are identical after canonical
// sorting: the same teams (rating_id/name/city/number/flags) in the same order, each
// with the same players (rating_id/first/last) in the same order. When true a
// re-import would rewrite every row to its current value and re-derive identical
// game state, so the whole import can be skipped. Callers must pass both sides
// through roster.SortedFestRosterImportTeams first.
func festRostersEqual(a, b []roster.FestRosterImportTeam) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !teamEqual(a[i], b[i]) || len(a[i].Players) != len(b[i].Players) {
			return false
		}
		for j := range a[i].Players {
			pa, pb := a[i].Players[j], b[i].Players[j]
			if pa.RatingID != pb.RatingID || pa.FirstName != pb.FirstName || pa.LastName != pb.LastName {
				return false
			}
		}
	}
	return true
}

// teamEqual compares two roster entries at the TEAM level: identity, name,
// city, number and Flags. Flags are part of it — an import that brings a new
// Flag must not be mistaken for a no-op, and must re-propagate the documents.
func teamEqual(a, b roster.FestRosterImportTeam) bool {
	if a.RatingID != b.RatingID || a.Name != b.Name || a.City != b.City || a.Country != b.Country || a.Number != b.Number {
		return false
	}
	if len(a.Flags) != len(b.Flags) {
		return false
	}
	for i := range a.Flags {
		if a.Flags[i].RatingID != b.Flags[i].RatingID || a.Flags[i].Short != b.Flags[i].Short || a.Flags[i].Full != b.Flags[i].Full {
			return false
		}
	}
	return true
}

// distinctPlayerCount counts unique players across an incoming roster the same
// way the rebuild dedups them (by rosterPlayerKey), so the no-op fast path can
// report an accurate player tally without touching the DB.
func distinctPlayerCount(teams []roster.FestRosterImportTeam) int {
	seen := make(map[string]struct{})
	for _, team := range teams {
		for _, player := range team.Players {
			seen[rosterPlayerKey(player)] = struct{}{}
		}
	}
	return len(seen)
}

// teamLevelEqual reports whether two rosters match at the TEAM level
// (rating_id/name/city/number/flags, same order), ignoring players. OD and KSI game
// state is a pure function of the team list, so when this holds their
// propagation would produce identical state and can be skipped. Both sides must
// be canonically sorted.
func teamLevelEqual(a, b []roster.FestRosterImportTeam) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !teamEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

// applyFestRosterDiffTx brings the canonical roster (fest_teams, fest_players,
// fest_team_players) to match `teams` by writing ONLY the rows that changed,
// instead of wiping and rebuilding. It keeps fest_players ids STABLE for players
// that stay (so game_player_team_overrides, which FK fest_players.id ON DELETE
// CASCADE, survive without the old preserve-by-rating/restore dance); a player
// dropped from the roster is deleted, and its override correctly cascades away.
// `teams` must be sorted and numbered (assignFestNumbersForImport). Produces the
// same end state as the former wipe-and-rebuild.
func applyFestRosterDiffTx(ctx context.Context, tx *sql.Tx, festID int64, teams []roster.FestRosterImportTeam, existingByRating map[int64]existingFestTeam, merges map[int64]int64) error {
	// Write the merges first. A team the host merged keeps its row, its number
	// and everything scored under it, and only changes the id the site knows it
	// by — so both rules below, the soft-delete of teams that left and the
	// hard-delete of rows with no id at all, see it as a team that stayed.
	for teamID, ratingID := range merges {
		if _, err := tx.ExecContext(ctx, `update fest_teams set rating_id = ? where id = ? and fest_id = ?`, ratingID, teamID, festID); err != nil {
			return err
		}
	}

	// --- Teams ---
	incomingRatingIDs := make(map[int64]struct{}, len(teams))
	for _, team := range teams {
		if team.RatingID > 0 {
			incomingRatingIDs[team.RatingID] = struct{}{}
		}
	}
	// Soft-delete rating teams that vanished from the incoming roster (their
	// number stays so they reappear if the team returns), and clear their roster
	// links so a soft-deleted team carries no stale players.
	for ratingID, existing := range existingByRating {
		if _, stays := incomingRatingIDs[ratingID]; stays {
			continue
		}
		if _, err := tx.ExecContext(ctx, `update fest_teams set deleted = 1 where id = ? and deleted = 0`, existing.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `delete from fest_team_players where team_id = ?`, existing.ID); err != nil {
			return err
		}
	}
	// Hard-delete rating_id-less rows — they can't be matched across syncs (their
	// fest_team_players cascade away with them).
	if _, err := tx.ExecContext(ctx, `delete from fest_teams where fest_id = ? and rating_id is null`, festID); err != nil {
		return err
	}

	// Upsert each incoming team in place, recording its fest_team id by position
	// (covers rating_id-less teams too, which always insert fresh).
	teamIDs := make([]int64, len(teams))
	for i, team := range teams {
		importOrder := i + 1
		var numberParam any
		if team.Number > 0 {
			numberParam = team.Number
		}
		if existing, ok := existingByRating[team.RatingID]; ok && team.RatingID > 0 {
			if _, err := tx.ExecContext(ctx, `
update fest_teams set name = ?, city = ?, country = ?, position = ?, number = ?, deleted = 0
 where id = ?`, team.Name, team.City, team.Country, importOrder, numberParam, existing.ID); err != nil {
				return err
			}
			teamIDs[i] = existing.ID
		} else {
			id, err := store.InsertReturningID(ctx, tx, `
insert into fest_teams(fest_id, rating_id, name, city, country, position, number, deleted)
values(?, ?, ?, ?, ?, ?, ?, 0)`, festID, util.NullableInt64(team.RatingID), team.Name, team.City, team.Country, importOrder, numberParam)
			if err != nil {
				return err
			}
			teamIDs[i] = id
		}
	}

	// A team's Flags are replaced wholesale on every import: the rating site is
	// the source of truth for the fest it was imported from, and a Flag has no
	// per-row history worth diffing.
	for i, team := range teams {
		if err := roster.ReplaceTeamFlagsTx(ctx, tx, teamIDs[i], team.Flags); err != nil {
			return err
		}
	}

	// --- Players pool (fest_players), stable ids ---
	type playerInfo struct {
		rating      int64
		first, last string
	}
	desired := make(map[string]playerInfo)
	for _, team := range teams {
		for _, p := range team.Players {
			desired[rosterPlayerKey(p)] = playerInfo{rating: p.RatingID, first: p.FirstName, last: p.LastName}
		}
	}
	type curPlayer struct {
		id          int64
		rating      int64
		first, last string
	}
	curByKey := make(map[string]curPlayer)
	cur, err := store.CollectRows(ctx, tx, `
select id, coalesce(rating_id, 0), first_name, last_name
from fest_players where fest_id = ?`, []any{festID}, func(rows *sql.Rows) (curPlayer, error) {
		var c curPlayer
		return c, rows.Scan(&c.id, &c.rating, &c.first, &c.last)
	})
	if err != nil {
		return err
	}
	for _, c := range cur {
		key := rosterPlayerKey(roster.FestRosterImportPlayer{RatingID: c.rating, FirstName: c.first, LastName: c.last})
		curByKey[key] = c
	}
	// Delete players no longer in the roster (cascades their fest_team_players and
	// any game_player_team_overrides — matching the old drop-on-remove behaviour).
	for key, c := range curByKey {
		if _, keep := desired[key]; keep {
			continue
		}
		if _, err := tx.ExecContext(ctx, `delete from fest_players where id = ?`, c.id); err != nil {
			return err
		}
	}
	// Keep / insert the rest, building key -> fest_player id.
	playerIDByKey := make(map[string]int64, len(desired))
	for key, info := range desired {
		if c, ok := curByKey[key]; ok {
			playerIDByKey[key] = c.id
			if c.rating != info.rating || c.first != info.first || c.last != info.last {
				if _, err := tx.ExecContext(ctx, `
update fest_players set rating_id = ?, first_name = ?, last_name = ? where id = ?`,
					util.NullableInt64(info.rating), info.first, info.last, c.id); err != nil {
					return err
				}
			}
			continue
		}
		id, err := store.InsertReturningID(ctx, tx, `
insert into fest_players(fest_id, rating_id, first_name, last_name) values(?, ?, ?, ?)`,
			festID, util.NullableInt64(info.rating), info.first, info.last)
		if err != nil {
			return err
		}
		playerIDByKey[key] = id
	}

	// --- fest_team_players, per team, row-level diff ---
	for i, team := range teams {
		teamID := teamIDs[i]
		desiredLinks := make(map[int64]int, len(team.Players))
		for order, p := range team.Players {
			desiredLinks[playerIDByKey[rosterPlayerKey(p)]] = order
		}
		type link struct {
			playerID int64
			order    int
		}
		curLinks, err := store.CollectRows(ctx, tx, `
select player_id, roster_order from fest_team_players where team_id = ?`, []any{teamID}, func(rows *sql.Rows) (link, error) {
			var l link
			return l, rows.Scan(&l.playerID, &l.order)
		})
		if err != nil {
			return err
		}
		curByID := make(map[int64]int, len(curLinks))
		for _, l := range curLinks {
			curByID[l.playerID] = l.order
			if _, want := desiredLinks[l.playerID]; !want {
				if _, err := tx.ExecContext(ctx, `delete from fest_team_players where team_id = ? and player_id = ?`, teamID, l.playerID); err != nil {
					return err
				}
			}
		}
		for pid, order := range desiredLinks {
			if curOrder, ok := curByID[pid]; ok {
				if curOrder != order {
					if _, err := tx.ExecContext(ctx, `update fest_team_players set roster_order = ? where team_id = ? and player_id = ?`, order, teamID, pid); err != nil {
						return err
					}
				}
				continue
			}
			if _, err := tx.ExecContext(ctx, `insert into fest_team_players(team_id, player_id, roster_order) values(?, ?, ?)`, teamID, pid, order); err != nil {
				return err
			}
		}
	}
	return nil
}

// assignFestNumbersForImport mutates teams in place so that:
//   - teams that already had a number (matched by rating_id, including
//     previously soft-deleted ones) keep it;
//   - every still-unnumbered team receives a fresh number strictly greater than
//     the largest one ever seen in this fest, in the (alphabetical) order of
//     incoming teams.
//
// Team number is the universal team identity across OD/KSI/EK (see the
// team-number unification), so every active team must always have one. On a
// first-ever import maxSeen is 0 and numbering starts at 1; on re-import,
// rating-matched teams keep their numbers and new teams continue past maxSeen
// (which counts soft-deleted rows too, so a returning team can't collide).
func assignFestNumbersForImport(teams []roster.FestRosterImportTeam, existing map[int64]existingFestTeam, maxSeen int64) {
	for i := range teams {
		teams[i].Number = 0
		if teams[i].RatingID > 0 {
			if e, ok := existing[teams[i].RatingID]; ok {
				teams[i].Number = e.Number
			}
		}
	}
	next := maxSeen + 1
	for i := range teams {
		if teams[i].Number == 0 {
			teams[i].Number = next
			next++
		}
	}
}

func rosterPlayerKey(player roster.FestRosterImportPlayer) string {
	if player.RatingID > 0 {
		return "rating:" + strconv.FormatInt(player.RatingID, 10)
	}
	return "name:" + strings.ToLower(store.JoinPlayerName(player.FirstName, player.LastName))
}
