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
	"dope/dope/domain/overrides"
	"dope/dope/domain/roster"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
)

const ratingResultsURL = "https://api.rating.chgk.net/tournaments/%d/results.json?includeTeamMembers=1"

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

func FetchAndImportRatingRoster(eng *core.Engine, ctx context.Context, festID, ratingID int64) (RatingRosterImportResult, error) {
	teams, err := fetchRatingFestRoster(ctx, ratingID)
	if err != nil {
		return RatingRosterImportResult{}, err
	}
	return ImportFestRoster(eng, ctx, festID, ratingID, teams)
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
		return nil, fmt.Errorf("не удалось запросить рейтинг: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		detail := strings.TrimSpace(string(body))
		if detail == "" {
			detail = resp.Status
		}
		return nil, fmt.Errorf("рейтинг вернул ошибку: %s", detail)
	}

	var results []ratingFestResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("не удалось разобрать ответ рейтинга: %w", err)
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
		city := ratingTownName(result.Current.Town)
		if city == "" {
			city = ratingTownName(result.Team.Town)
		}
		team := roster.FestRosterImportTeam{
			RatingID: result.Team.ID,
			Name:     name,
			City:     city,
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
		if len(team.Players) > 9 {
			return nil, fmt.Errorf("состав %q больше 9 игроков", name)
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

func ImportFestRoster(eng *core.Engine, ctx context.Context, festID, ratingID int64, teams []roster.FestRosterImportTeam) (RatingRosterImportResult, error) {
	if eng.DB == nil {
		return RatingRosterImportResult{}, errors.New("sqlite is not enabled")
	}
	if festID <= 0 {
		return RatingRosterImportResult{}, errors.New("bad fest id")
	}
	if len(teams) == 0 {
		return RatingRosterImportResult{}, errors.New("рейтинг не вернул команды")
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

		existingByRating, maxSeenNumber, err := loadFestExistingTeams(ctx, conn, festID)
		if err != nil {
			return RatingRosterImportResult{}, err
		}
		assignFestNumbersForImport(teams, existingByRating, maxSeenNumber)

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
		if err := applyFestRosterDiffTx(ctx, tx, festID, teams, existingByRating); err != nil {
			return RatingRosterImportResult{}, err
		}
		playerCount := distinctPlayerCount(teams)

		// OD/KSI game state is a pure function of the TEAM list, so only re-propagate
		// when teams actually changed — a player-only change leaves it identical.
		var chgkUpdates, ksiUpdates []roster.GameStateBroadcast
		if !teamLevelEqual(sortedCurrent, teams) {
			chgkUpdates, err = roster.PropagateRosterToChGKTx(ctx, tx, festID, teams, nil)
			if err != nil {
				return RatingRosterImportResult{}, err
			}
			ksiUpdates, err = roster.PropagateRosterToKSITx(ctx, tx, festID, teams)
			if err != nil {
				return RatingRosterImportResult{}, err
			}
		}
		updates = append(chgkUpdates, ksiUpdates...)

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
			"odGames":  len(chgkUpdates),
			"ksiGames": len(ksiUpdates),
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
			ODGameCount:  len(chgkUpdates),
			KSIGameCount: len(ksiUpdates),
		}, nil
	}()
	if err != nil {
		return RatingRosterImportResult{}, err
	}

	for _, update := range updates {
		eng.BroadcastState(festID, fmt.Sprintf("game-state:%d", update.GameID), revision, update.StateJSON)
	}
	for _, gameID := range ekOverrideGameIDs {
		eng.BroadcastState(festID, fmt.Sprintf("game-roster:%d", gameID), revision, []byte(`{}`))
	}
	return result, nil
}

type existingFestTeam struct {
	ID     int64
	Number int64
}

// loadFestExistingTeams returns the rating_id → row mapping for every fest_team
// in this fest (including soft-deleted ones, so that previously archived
// numbers can be restored when a team is re-added). maxSeenNumber is the
// largest number ever assigned in this fest — new teams introduced by a
// re-sync always receive numbers strictly greater than this, so already-printed
// answer sheets keep referring to the right team.
func loadFestExistingTeams(ctx context.Context, tx store.Queryer, festID int64) (map[int64]existingFestTeam, int64, error) {
	rows, err := tx.QueryContext(ctx, `
select id, coalesce(rating_id, 0), coalesce(number, 0)
from fest_teams
where fest_id = ?`, festID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	byRating := make(map[int64]existingFestTeam)
	var maxNum int64
	for rows.Next() {
		var id, ratingID, number int64
		if err := rows.Scan(&id, &ratingID, &number); err != nil {
			return nil, 0, err
		}
		if ratingID > 0 {
			byRating[ratingID] = existingFestTeam{ID: id, Number: number}
		}
		if number > maxNum {
			maxNum = number
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return byRating, maxNum, nil
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
		})
	}
	return out, nil
}

// festRostersEqual reports whether two rosters are identical after canonical
// sorting: the same teams (rating_id/name/city/number) in the same order, each
// with the same players (rating_id/first/last) in the same order. When true a
// re-import would rewrite every row to its current value and re-derive identical
// game state, so the whole import can be skipped. Callers must pass both sides
// through roster.SortedFestRosterImportTeams first.
func festRostersEqual(a, b []roster.FestRosterImportTeam) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].RatingID != b[i].RatingID || a[i].Name != b[i].Name ||
			a[i].City != b[i].City || a[i].Number != b[i].Number ||
			len(a[i].Players) != len(b[i].Players) {
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
// (rating_id/name/city/number, same order), ignoring players. OD and KSI game
// state is a pure function of the team list, so when this holds their
// propagation would produce identical state and can be skipped. Both sides must
// be canonically sorted.
func teamLevelEqual(a, b []roster.FestRosterImportTeam) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].RatingID != b[i].RatingID || a[i].Name != b[i].Name ||
			a[i].City != b[i].City || a[i].Number != b[i].Number {
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
func applyFestRosterDiffTx(ctx context.Context, tx *sql.Tx, festID int64, teams []roster.FestRosterImportTeam, existingByRating map[int64]existingFestTeam) error {
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
update fest_teams set name = ?, city = ?, position = ?, number = ?, deleted = 0
 where id = ?`, team.Name, team.City, importOrder, numberParam, existing.ID); err != nil {
				return err
			}
			teamIDs[i] = existing.ID
		} else {
			id, err := store.InsertReturningID(ctx, tx, `
insert into fest_teams(fest_id, rating_id, name, city, position, number, deleted)
values(?, ?, ?, ?, ?, ?, 0)`, festID, util.NullableInt64(team.RatingID), team.Name, team.City, importOrder, numberParam)
			if err != nil {
				return err
			}
			teamIDs[i] = id
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
