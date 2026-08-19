// Package resolver propagates results forward through a game's bracket: it
// fills from_match/reseed slots once their upstream sources are final and
// (re)computes reseed-stage standings. It is a leaf package — it depends only
// on the store/util helpers and the standard library, never on the server.
package resolver

import (
	"context"
	"database/sql"
	"dope/dope/domain/structure"
	"dope/dope/storage/store"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrReseedStageNotFound = errors.New("reseed stage not found")
	errReseedNotReady      = errors.New("пересев можно рассчитать после завершения всех исходных боёв")
)

// ErrReseedNotReady is returned (via reseedNotReadyError) when an explicit
// reseed calculation is attempted before all of its source bouts are finished.
var ErrReseedNotReady = errReseedNotReady

type reseedNotReadyError struct {
	pending []string
}

func (e reseedNotReadyError) Error() string {
	return ReseedNotReadyMessage(e.pending)
}

func (e reseedNotReadyError) Is(target error) bool {
	return target == errReseedNotReady
}

type reseedResolveMode int

const (
	reseedInvalidateOnly reseedResolveMode = iota
	reseedCalculateOne
	reseedCalculateAll
)

// nullableInt64 maps a zero id to a SQL NULL and any other value to itself.
func nullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

// ResolveGameSlotsTx propagates results forward through a game's bracket. It
// runs inside the write transaction after a match's results are recalculated,
// and is responsible for the parts of the bracket that depend on other matches:
//
//   - reseed stages: update readiness. Held reseed_entries are not deleted when a
//     source bout goes temporarily un-final (so untick/retick doesn't wipe them);
//     live match edits do not create reseed_entries, an explicit calculate does.
//   - from_match / reseed slots: fill match_slots.participant_id once the upstream
//     source is final, and (for EK) create that team's themes.
//
// It is idempotent and non-destructive: a slot is only rewritten when its
// resolved occupant changes to a different concrete team. A source that goes
// temporarily unresolved (e.g. unticked for editing) holds its slot rather than
// flushing it, and an occupant change reopens the bout without deleting the
// previous occupant's protocol data. See applyResolvedSlotTx.
// ResolveGameSlotsTx resolves every from_match/reseed slot in the game and
// returns the ids of matches whose slots actually changed — so a caller can
// broadcast those downstream matches (a finished bout advances teams into the
// next round, which would otherwise only show up on a viewer reload).
func ResolveGameSlotsTx(ctx context.Context, tx *sql.Tx, gameID int64) ([]int64, error) {
	return resolveGameSlotsWithReseedModeTx(ctx, tx, gameID, reseedInvalidateOnly, "")
}

// ResolveGameSlotsAndReseedsTx is the maintenance form used by the CLI: it
// reconciles every ready reseed stage instead of requiring a UI button press.
func ResolveGameSlotsAndReseedsTx(ctx context.Context, tx *sql.Tx, gameID int64) ([]int64, error) {
	return resolveGameSlotsWithReseedModeTx(ctx, tx, gameID, reseedCalculateAll, "")
}

// CalculateReseedStageSlotsTx calculates one reseed stage and then resolves
// every downstream slot that depends on it.
func CalculateReseedStageSlotsTx(ctx context.Context, tx *sql.Tx, gameID int64, stageCode string) ([]int64, error) {
	return resolveGameSlotsWithReseedModeTx(ctx, tx, gameID, reseedCalculateOne, stageCode)
}

func resolveGameSlotsWithReseedModeTx(ctx context.Context, tx *sql.Tx, gameID int64, mode reseedResolveMode, targetStageCode string) ([]int64, error) {
	var gameType string
	if err := tx.QueryRowContext(ctx, `select game_type from games where id = ?`, gameID).Scan(&gameType); err != nil {
		return nil, err
	}

	stages, err := store.CollectRows(ctx, tx, `
select id, code, stage_type, kind, status, config_json
from stages where game_id = ? order by position, id`,
		[]any{gameID}, func(rows *sql.Rows) (resolverStage, error) {
			var st resolverStage
			return st, rows.Scan(&st.id, &st.code, &st.stageType, &st.kind, &st.status, &st.config)
		})
	if err != nil {
		return nil, err
	}

	var affected []int64
	seen := map[int64]bool{}
	foundTarget := mode != reseedCalculateOne
	for _, stage := range stages {
		if stage.stageType == "reseed" {
			var err error
			switch {
			case mode == reseedCalculateAll:
				err = calculateReadyReseedEntriesTx(ctx, tx, stage, gameID)
			case mode == reseedCalculateOne && stage.code == targetStageCode:
				foundTarget = true
				err = calculateRequiredReseedEntriesTx(ctx, tx, stage, gameID)
			default:
				err = syncReseedReadinessTx(ctx, tx, stage, gameID)
			}
			if err != nil {
				return nil, err
			}
		} else if isRankedKind(stage.kind) {
			// A registered Stage Kind (rr group, elimination bracket, …) ranks
			// live on every recompute: standings display updates as bouts land;
			// downstream rank slots stay gated on stage completeness (see
			// teamAtReseedRank).
			if err := recomputeKindStandingsTx(ctx, tx, stage, gameID); err != nil {
				return nil, err
			}
		}
		changed, err := resolveStageSlotsTx(ctx, tx, gameID, stage.id, gameType)
		if err != nil {
			return nil, err
		}
		for _, id := range changed {
			if !seen[id] {
				seen[id] = true
				affected = append(affected, id)
			}
		}
	}
	if !foundTarget {
		return nil, ErrReseedStageNotFound
	}
	return affected, nil
}

type resolverStage struct {
	id        int64
	code      string
	stageType string
	kind      string
	status    string
	config    []byte
}

// RanksItsOwnStage reports whether a stage's kind computes its own Standings —
// what lets a view show a Group as a table rather than as a wall of бои.
func RanksItsOwnStage(kind string) bool { return isRankedKind(kind) }

// isRankedKind reports whether a stage's kind ranks itself live on every
// recompute — every registered Ranker but the reseed, which ranks on the
// host's explicit calculate.
func isRankedKind(kind string) bool {
	if kind == "reseed" {
		return false // ranked too, but on the host's explicit calculate
	}
	_, ok := structure.RankerFor(kind)
	return ok
}

// recomputeKindStandingsTx ranks a kind stage from its matches' current
// results and replaces its stage_standings rows.
func recomputeKindStandingsTx(ctx context.Context, tx *sql.Tx, stage resolverStage, gameID int64) error {
	ranker, ok := structure.RankerFor(stage.kind)
	if !ok {
		return nil
	}
	outcomes, _, err := stageMatchOutcomesTx(ctx, tx, stage.id)
	if err != nil {
		return err
	}
	seed, err := gameRandomSeed(ctx, tx, gameID)
	if err != nil {
		return err
	}
	ranked, err := ranker.Standings(KindConfig(stage.config), outcomes, structure.Inputs{Seed: seed})
	if err != nil {
		return fmt.Errorf("stage %s standings: %w", stage.code, err)
	}
	return store.WriteStandings(ctx, tx, stage.id, ranked)
}

// KindConfig is a stage's config as its Kind reads it (store.StageConfig).
func KindConfig(raw []byte) json.RawMessage { return store.ParseStageConfig(string(raw)).KindConfig() }

// stageMatchOutcomesTx loads a stage's matches as structure.MatchOutcome (slot
// results in slot order) and reports whether every match is finished.
func stageMatchOutcomesTx(ctx context.Context, tx *sql.Tx, stageID int64) ([]structure.MatchOutcome, bool, error) {
	ids, err := store.CollectRows(ctx, tx, `select id from matches where stage_id = ? order by position, id`,
		[]any{stageID}, func(rows *sql.Rows) (int64, error) {
			var id int64
			return id, rows.Scan(&id)
		})
	if err != nil {
		return nil, false, err
	}
	outcomes, err := store.LoadMatchOutcomes(ctx, tx, ids)
	if err != nil {
		return nil, false, err
	}
	allFinished := true
	for _, outcome := range outcomes {
		allFinished = allFinished && outcome.Finished
	}
	return outcomes, allFinished, nil
}

// resolveStageSlotsTx resolves every from_match/reseed slot of one stage and
// returns the ids of matches whose slots changed.
func resolveStageSlotsTx(ctx context.Context, tx *sql.Tx, gameID, stageID int64, gameType string) ([]int64, error) {
	type slotRow struct {
		id         int64
		matchID    int64
		sourceType string
		sourceRef  string
		teamID     int64
	}
	slots, err := store.CollectRows(ctx, tx, `
select ms.id, ms.match_id, ms.source_type, ms.source_ref_json, coalesce(ms.participant_id, 0)
from match_slots ms
join matches m on m.id = ms.match_id
where m.stage_id = ? and ms.locked = 0 and ms.source_type in ('from_match', 'reseed')
order by ms.match_id, ms.slot_index`,
		[]any{stageID}, func(rows *sql.Rows) (slotRow, error) {
			var s slotRow
			return s, rows.Scan(&s.id, &s.matchID, &s.sourceType, &s.sourceRef, &s.teamID)
		})
	if err != nil {
		return nil, err
	}

	src := dbSources{ctx, tx, gameID}
	var affected []int64
	for _, slot := range slots {
		desired, err := desiredOccupant(src, store.ParseSlotRef(slot.sourceType, slot.sourceRef))
		if err != nil {
			return nil, err
		}
		changed, err := applyResolvedSlotTx(ctx, tx, slot.id, slot.matchID, slot.teamID, desired, gameType)
		if err != nil {
			return nil, err
		}
		if changed {
			affected = append(affected, slot.matchID)
		}
	}
	return affected, nil
}

// applyResolvedSlotTx writes a slot's resolved occupant per slotTransition and
// reports whether it changed (so the caller can collect the affected match for
// broadcast).
func applyResolvedSlotTx(ctx context.Context, tx *sql.Tx, slotID, matchID, current, desired int64, gameType string) (bool, error) {
	move, reopen := slotTransition(current, desired)
	if !move {
		return false, nil
	}
	if reopen {
		if _, err := tx.ExecContext(ctx, `update matches set status = 'active' where id = ? and status = 'finished'`, matchID); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `update match_slots set participant_id = ? where id = ?`, nullableInt64(desired), slotID); err != nil {
		return false, err
	}
	return true, nil
}

// --- reseed computation --------------------------------------------------

type reseedConfig = store.StageConfig

func syncReseedReadinessTx(ctx context.Context, tx *sql.Tx, stage resolverStage, gameID int64) error {
	state, err := ReseedPrerequisites(ctx, tx, stage.config, gameID)
	if err != nil {
		return err
	}
	if !state.Ready {
		// HOLD: a source bout is temporarily un-final (e.g. unticked for editing).
		// Keep the previously-calculated reseed_entries rather than deleting them,
		// so untick→retick doesn't wipe the reseed. The next explicit calculate
		// refreshes them if a correction genuinely changed who advances. (The view
		// recomputes ReseedReady live from prerequisites, so the UI still shows the
		// pending/ready state correctly without us downgrading stage status here.)
		return nil
	}
	if stage.status == "pending" {
		_, err := tx.ExecContext(ctx, `update stages set status = 'active' where id = ?`, stage.id)
		return err
	}
	return nil
}

func calculateReadyReseedEntriesTx(ctx context.Context, tx *sql.Tx, stage resolverStage, gameID int64) error {
	state, err := ReseedPrerequisites(ctx, tx, stage.config, gameID)
	if err != nil {
		return err
	}
	if !state.Ready {
		return syncReseedReadinessTx(ctx, tx, stage, gameID)
	}
	if err := recomputeReseedEntriesTx(ctx, tx, stage.id, stage.config, gameID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `update stages set status = 'finished' where id = ?`, stage.id)
	return err
}

func calculateRequiredReseedEntriesTx(ctx context.Context, tx *sql.Tx, stage resolverStage, gameID int64) error {
	state, err := ReseedPrerequisites(ctx, tx, stage.config, gameID)
	if err != nil {
		return err
	}
	if !state.Ready {
		return reseedNotReadyError{pending: state.PendingMatches}
	}
	if err := recomputeReseedEntriesTx(ctx, tx, stage.id, stage.config, gameID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `update stages set status = 'finished' where id = ?`, stage.id)
	return err
}

// ReseedPrerequisites reports whether a reseed stage's source bouts are all
// finished, listing the source bout ids and the codes of any still-pending ones.
func ReseedPrerequisites(ctx context.Context, q store.Queryer, config []byte, gameID int64) (reseedPrerequisiteState, error) {
	cfg := store.ParseStageConfig(string(config))
	bouts, err := reseedSourceBouts(ctx, q, gameID, cfg)
	if err != nil {
		return reseedPrerequisiteState{}, err
	}
	return prerequisites(dbSources{ctx, q, gameID}, cfg, bouts)
}

// ReseedNotReadyMessage formats a human-facing message naming the source bouts
// that still need to finish before a reseed can be calculated.
func ReseedNotReadyMessage(pending []string) string {
	codes := make([]string, 0, len(pending))
	for _, code := range pending {
		code = strings.TrimSpace(code)
		if code != "" {
			codes = append(codes, code)
		}
	}
	switch len(codes) {
	case 0:
		return errReseedNotReady.Error()
	case 1:
		return fmt.Sprintf("Бой %s не закончен", codes[0])
	default:
		return fmt.Sprintf("Бои %s не закончены", strings.Join(codes, ", "))
	}
}

// recomputeReseedEntriesTx rebuilds a reseed stage's table: the resolver
// names who advances (the place selectors in `teams`, each with its band) and
// which бои count (the `sources` stages, else the бой each team advanced
// from), and the reseed Ranker sums, orders and lots them. The table is
// cleared until every source бой is finished, so downstream reseed slots stay
// unresolved until then.
func recomputeReseedEntriesTx(ctx context.Context, tx *sql.Tx, stageID int64, config []byte, gameID int64) error {
	cfg := store.ParseStageConfig(string(config))
	src := dbSources{ctx, tx, gameID}
	who, ok, err := contenders(src, cfg)
	if err != nil {
		return err
	}
	if !ok {
		return store.WriteStandings(ctx, tx, stageID, nil) // a source bout is not finished yet
	}
	state, err := ReseedPrerequisites(ctx, tx, config, gameID)
	if err != nil {
		return err
	}
	if !state.Ready {
		return store.WriteStandings(ctx, tx, stageID, nil)
	}
	outcomes, err := store.LoadMatchOutcomes(ctx, tx, state.SourceMatchIDs)
	if err != nil {
		return err
	}
	// Жребий lots are derived from the game's fixed random seed, so a tie
	// always breaks the same way no matter how many times the reseed is
	// recomputed — re-finishing an edited source bout can never reshuffle it.
	seed, err := gameRandomSeed(ctx, tx, gameID)
	if err != nil {
		return err
	}
	ranker, _ := structure.RankerFor("reseed")
	ranked, err := ranker.Standings(KindConfig(config), outcomes, structure.Inputs{Seed: seed, Contenders: who})
	if err != nil {
		return err
	}
	return store.WriteStandings(ctx, tx, stageID, ranked)
}

// gameRandomSeed returns the game's fixed random seed (the basis for deterministic
// reseed lots). Falls back to the game id when the column is empty so an unseeded
// game is still deterministic.
func gameRandomSeed(ctx context.Context, q store.Queryer, gameID int64) (string, error) {
	var seed sql.NullString
	err := q.QueryRowContext(ctx, `select random_seed from games where id = ?`, gameID).Scan(&seed)
	if err != nil {
		return "", err
	}
	if seed.Valid && seed.String != "" {
		return seed.String, nil
	}
	return fmt.Sprintf("game-%d", gameID), nil
}

// reseedSourceBouts returns the бои that contribute to a reseed: the `sources`
// stages' бои when named, else the бой each team advances from.
func reseedSourceBouts(ctx context.Context, q store.Queryer, gameID int64, cfg reseedConfig) ([]Bout, error) {
	scan := func(rows *sql.Rows) (Bout, error) {
		var b Bout
		return b, rows.Scan(&b.ID, &b.Code, &b.Status)
	}
	if len(cfg.Sources) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(cfg.Sources)), ",")
		args := []any{gameID}
		for _, code := range cfg.Sources {
			args = append(args, code)
		}
		return store.CollectRows(ctx, q, fmt.Sprintf(`
select m.id, m.code, m.status from matches m
join stages s on s.id = m.stage_id
where m.game_id = ? and s.code in (%s)
order by s.position, m.position, m.id`, placeholders), args, scan)
	}
	codes := make(map[string]struct{})
	for _, slot := range cfg.Teams {
		if slot.FromMatch != nil {
			codes[slot.FromMatch.Match] = struct{}{}
		}
	}
	if len(codes) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(codes)), ",")
	args := []any{gameID}
	orderedCodes := make([]string, 0, len(codes))
	for code := range codes {
		orderedCodes = append(orderedCodes, code)
	}
	sort.Strings(orderedCodes)
	for _, code := range orderedCodes {
		args = append(args, code)
	}
	return store.CollectRows(ctx, q, fmt.Sprintf(`
select id, code, status from matches where game_id = ? and code in (%s)
order by position, id`, placeholders), args, scan)
}
