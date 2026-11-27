package roster

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"dope/dope/domain/games"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
)

// Where a flat Game's teams come from (ADR-0038): the fest's teams, another
// Game's teams, or its separate list; then its overrides. A Game that takes
// the fest's teams and has no override is "plain" and is folded the way it
// always was; every other Game is worked out here, and a change reaches the
// Games that follow it in dependency order.

// teamsSource is a Game's source, as stored. numbers is each team's number
// at the last fold, by key, which carries answers to a team's new number.
type teamsSource struct {
	gameID    int64
	venue     int64
	own       bool
	from      int64
	list      []OwnTeam
	overrides []Override
	numbers   map[string]int64
}

func (s teamsSource) plain() bool {
	return !s.own && s.from == 0 && len(s.overrides) == 0
}

// loadSource reads a Game's source; a Game not made yet (gameID 0) takes the
// fest's teams.
func loadSource(ctx context.Context, q store.Queryer, gameID int64) (teamsSource, error) {
	src := teamsSource{gameID: gameID}
	if gameID == 0 {
		return src, nil
	}
	var list, overrides, numbers string
	err := q.QueryRowContext(ctx, `
select coalesce(multifest_venue_id, 0), own_teams, teams_from, own_roster, overrides, roster_numbers from games where id = ?`, gameID).
		Scan(&src.venue, &src.own, &src.from, &list, &overrides, &numbers)
	if err != nil {
		return src, err
	}
	for _, field := range []struct {
		raw  string
		into any
	}{{list, &src.list}, {overrides, &src.overrides}, {numbers, &src.numbers}} {
		if field.raw != "" {
			if err := json.Unmarshal([]byte(field.raw), field.into); err != nil {
				return src, fmt.Errorf("game %d: %w", gameID, err)
			}
		}
	}
	return src, nil
}

// storeSource writes a Game's source but its numbers.
func storeSource(ctx context.Context, tx *sql.Tx, src teamsSource) error {
	list := make([]OwnTeam, len(src.list))
	for i, team := range src.list {
		list[i] = team.stored()
	}
	overrides := make([]Override, len(src.overrides))
	for i, o := range src.overrides {
		o.Applied = false
		overrides[i] = o
	}
	rawList, err := json.Marshal(list)
	if err != nil {
		return err
	}
	rawOverrides, err := json.Marshal(overrides)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `update games set own_teams = ?, teams_from = ?, own_roster = ?, overrides = ? where id = ?`,
		src.own, src.from, string(rawList), string(rawOverrides), src.gameID)
	return err
}

func storeNumbers(ctx context.Context, tx *sql.Tx, gameID int64, numbers map[string]int64) error {
	raw, err := json.Marshal(numbers)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `update games set roster_numbers = ? where id = ?`, string(raw), gameID)
	return err
}

// resolveTeams is the teams a Game's source gives it with its overrides
// applied; the overrides come back marked applied or not. seen holds the
// Games already on the way, which a loop would come back to.
func resolveTeams(ctx context.Context, q store.Queryer, festID int64, src teamsSource, seen map[int64]bool) ([]OwnTeam, []Override, error) {
	if src.own {
		return append([]OwnTeam(nil), src.list...), nil, nil
	}
	var base []OwnTeam
	if src.from > 0 {
		if seen[src.from] {
			return nil, nil, corei18n.User(dopestrings.Default.Entrants.Own.Cycle())
		}
		seen[src.gameID] = true
		upstream, err := loadSource(ctx, q, src.from)
		if err != nil {
			return nil, nil, err
		}
		if base, _, err = resolveTeams(ctx, q, festID, upstream, seen); err != nil {
			return nil, nil, err
		}
		for i := range base {
			base[i].Overridden, base[i].Missing = nil, false
		}
	} else {
		teams, err := festSeated(ctx, q, festID, src.venue)
		if err != nil {
			return nil, nil, err
		}
		base = OwnTeams(teams)
	}
	overrides := append([]Override(nil), src.overrides...)
	return applyOverrides(base, overrides), overrides, nil
}

// seatPlan is the teams a Game seats after a change: the resolved ones and
// any team with answers that left them, the answers' moves to new numbers,
// and why the Game cannot take the change, if it cannot.
type seatPlan struct {
	teams    []OwnTeam
	remap    map[int]int
	numbers  map[string]int64
	conflict string
}

// planSeats keeps a team the Game holds answers for when it leaves the
// source, under its last name and number, and moves the answers of a team
// whose number changed.
func planSeats(resolved []OwnTeam, prev map[string]int64, doc store.GameDoc) seatPlan {
	plan := seatPlan{teams: resolved, remap: map[int]int{}, numbers: map[string]int64{}}
	was := map[int64]bool{}
	byNumber := map[int64]string{}
	held := map[int64]bool{}
	for _, n := range prev {
		held[n] = true
	}
	for _, team := range resolved {
		// A team the last fold did not see held its number only when no
		// other team had it then.
		old, known := prev[team.Key]
		if !known && !held[team.Number] {
			old = team.Number
		}
		was[old] = true
		if old > 0 && old != team.Number {
			plan.remap[int(old)] = int(team.Number)
		}
		byNumber[team.Number] = team.Name
		plan.numbers[team.Key] = team.Number
	}
	for _, seat := range enteredSeats(doc) {
		if was[seat.Number] {
			continue
		}
		if name, taken := byNumber[seat.Number]; taken {
			plan.conflict = dopestrings.Default.Entrants.Own.NumberConflict(fmt.Sprint(seat.Number), seat.Name, name)
			return plan
		}
		key := heldKey(prev, seat.Number)
		plan.teams = append(plan.teams, OwnTeam{Key: key, Number: seat.Number, Name: seat.Name, City: seat.City, Missing: true})
		plan.numbers[key] = seat.Number
	}
	return plan
}

// heldKey is the key of a team kept for its answers: the one it had.
func heldKey(prev map[string]int64, number int64) string {
	for key, n := range prev {
		if n == number {
			return key
		}
	}
	return fmt.Sprintf("h%d", number)
}

// enteredSeats is the Game's seats it holds answers for.
func enteredSeats(doc store.GameDoc) []games.Seat {
	counter, ok := games.As[games.Entered](doc.GameType)
	if !ok {
		return nil
	}
	state := json.RawMessage(doc.State)
	entered, seats := counter.EnteredSeats(state), counter.Seats(state)
	var out []games.Seat
	for i, seat := range seats {
		if i < len(entered) && entered[i] && seat.Number > 0 {
			out = append(out, seat)
		}
	}
	return out
}

// gamePlan works a Game's teams out as they would be seated now.
func gamePlan(ctx context.Context, q store.Queryer, festID int64, doc store.GameDoc) (seatPlan, []Override, error) {
	src, err := loadSource(ctx, q, doc.GameID)
	if err != nil {
		return seatPlan{}, nil, err
	}
	resolved, overrides, err := resolveTeams(ctx, q, festID, src, map[int64]bool{})
	if err != nil {
		return seatPlan{}, nil, err
	}
	return planSeats(resolved, src.numbers, doc), overrides, nil
}

// refoldTx seats a Game's teams as they are now. A Game that cannot take the
// change (a number its answers need is another team's) is left as it was;
// its tab says why.
func refoldTx(ctx context.Context, tx *sql.Tx, festID int64, doc store.GameDoc) ([]GameStateBroadcast, error) {
	plan, _, err := gamePlan(ctx, tx, festID, doc)
	if err != nil || plan.conflict != "" {
		return nil, err
	}
	update, err := foldGameTx(ctx, tx, festID, doc, RosterTeams(importTeams(plan.teams)), Remap{Venue: doc.MultiFestVenueID, Numbers: plan.remap})
	if err != nil {
		return nil, err
	}
	return []GameStateBroadcast{update}, storeNumbers(ctx, tx, doc.GameID, plan.numbers)
}

// keepNumbers records a Game's numbers as they stand before a change to its
// teams, which the fold after it moves answers by.
func keepNumbers(ctx context.Context, tx *sql.Tx, festID int64, doc store.GameDoc) error {
	plan, _, err := gamePlan(ctx, tx, festID, doc)
	if err != nil {
		return err
	}
	return storeNumbers(ctx, tx, doc.GameID, plan.numbers)
}

// refoldWithFollowersTx refolds a Game, then every Game that follows it, at
// any distance, in order, and every tethered bout game, which may follow one
// of them. A bout game's update names the game and carries no document.
func refoldWithFollowersTx(ctx context.Context, tx *sql.Tx, festID int64, doc store.GameDoc) ([]GameStateBroadcast, error) {
	if BoutTeamGame(doc.GameType) {
		src, err := loadSource(ctx, tx, doc.GameID)
		if err != nil {
			return nil, err
		}
		return []GameStateBroadcast{{GameID: doc.GameID, GameType: doc.GameType}}, refreshBoutTx(ctx, tx, festID, doc, src)
	}
	out, err := refoldTx(ctx, tx, festID, doc)
	if err != nil {
		return nil, err
	}
	more, err := refoldFollowersTx(ctx, tx, festID, doc.GameID, false)
	if err != nil {
		return nil, err
	}
	bouts, err := RefreshBoutGamesTx(ctx, tx, festID)
	for _, id := range bouts {
		more = append(more, GameStateBroadcast{GameID: id})
	}
	return append(out, more...), err
}

// refoldFollowersTx refolds the Games that follow the Game changed (0: the
// fest's teams), nearest first. skipPlain leaves out the plain Games, which a
// change of the fest's teams folds itself.
func refoldFollowersTx(ctx context.Context, tx *sql.Tx, festID, changed int64, skipPlain bool) ([]GameStateBroadcast, error) {
	docs, err := store.LoadGameDocs(ctx, tx, festID)
	if err != nil {
		return nil, err
	}
	followers := map[int64][]store.GameDoc{}
	sources := map[int64]teamsSource{}
	for _, doc := range docs {
		if _, ok := games.As[games.RosterFolder](doc.GameType); !ok {
			continue
		}
		src, err := loadSource(ctx, tx, doc.GameID)
		if err != nil {
			return nil, err
		}
		sources[doc.GameID] = src
		if !src.own {
			followers[src.from] = append(followers[src.from], doc)
		}
	}
	var out []GameStateBroadcast
	queue, done := []int64{changed}, map[int64]bool{changed: true}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, doc := range followers[next] {
			if done[doc.GameID] {
				continue
			}
			done[doc.GameID] = true
			queue = append(queue, doc.GameID)
			if skipPlain && sources[doc.GameID].plain() {
				continue
			}
			updates, err := refoldTx(ctx, tx, festID, doc)
			if err != nil {
				return nil, err
			}
			out = append(out, updates...)
		}
	}
	return out, nil
}
