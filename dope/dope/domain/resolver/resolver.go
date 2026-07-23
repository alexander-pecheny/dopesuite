// Package resolver propagates results forward through a game's bracket: it
// fills from_match/reseed slots once their upstream sources are final and
// (re)computes reseed-stage standings. It is a leaf package — it depends only
// on the store/util helpers and the standard library, never on the server.
package resolver

import (
	"context"
	"database/sql"
	"dope/dope/domain/structure"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
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
//   - from_match / reseed slots: fill match_slots.team_id once the upstream
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

// isRankedKind reports whether a stage's kind carries its own Standings
// computation (everything registered except the manual list and the
// calculate-gated reseed, which keep their legacy flows).
func isRankedKind(kind string) bool {
	if kind == "" || kind == "matches" || kind == "reseed" {
		return false
	}
	_, ok := structure.Kind(kind)
	return ok
}

// recomputeKindStandingsTx ranks a kind stage from its matches' current
// results and replaces its stage_standings rows.
func recomputeKindStandingsTx(ctx context.Context, tx *sql.Tx, stage resolverStage, gameID int64) error {
	kind, ok := structure.Kind(stage.kind)
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
	ranked, err := kind.Standings(kindStageConfig(stage.config, seed), outcomes)
	if err != nil {
		return fmt.Errorf("stage %s standings: %w", stage.code, err)
	}
	if _, err := tx.ExecContext(ctx, `delete from stage_standings where stage_id = ?`, stage.id); err != nil {
		return err
	}
	for seat, entry := range ranked {
		// The stored rank is the distinct seat order (rank refs must resolve
		// uniquely); the kind's shared display place travels in the metrics.
		metrics := map[string]float64{"place": float64(entry.Rank)}
		for key, value := range entry.Metrics {
			metrics[key] = value
		}
		if _, err := tx.ExecContext(ctx, `
insert into stage_standings(stage_id, rank, participant_id, metrics_json)
values(?, ?, ?, ?)`, stage.id, seat+1, entry.Participant, util.MustJSON(metrics)); err != nil {
			return err
		}
	}
	return nil
}

// kindStageConfig unwraps the persisted stage config (storeutil nests the
// scheme's config under "config") and injects the game's deterministic seed
// for tie lots.
func kindStageConfig(raw []byte, seed string) json.RawMessage {
	var outer struct {
		Config map[string]any `json:"config"`
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(raw, &outer); err == nil && outer.Config != nil {
		cfg = outer.Config
	} else {
		_ = json.Unmarshal(raw, &cfg)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	cfg["seed"] = seed
	return json.RawMessage(util.MustJSON(cfg))
}

// stageMatchOutcomesTx loads a stage's matches as structure.MatchOutcome (slot
// results in slot order) and reports whether every match is finished.
func stageMatchOutcomesTx(ctx context.Context, tx *sql.Tx, stageID int64) ([]structure.MatchOutcome, bool, error) {
	type matchRow struct {
		id     int64
		code   string
		status string
	}
	matches, err := store.CollectRows(ctx, tx, `
select id, code, status from matches where stage_id = ? order by position, id`,
		[]any{stageID}, func(rows *sql.Rows) (matchRow, error) {
			var m matchRow
			return m, rows.Scan(&m.id, &m.code, &m.status)
		})
	if err != nil {
		return nil, false, err
	}
	allFinished := true
	outcomes := make([]structure.MatchOutcome, 0, len(matches))
	for _, m := range matches {
		finished := m.status == "finished"
		if !finished {
			allFinished = false
		}
		type slotRes struct {
			teamID  int64
			place   float64
			total   float64
			plus    float64
			metrics string
		}
		slots, err := store.CollectRows(ctx, tx, `
select coalesce(ms.team_id, 0), coalesce(mr.place, 0), coalesce(mr.total, 0), coalesce(mr.plus, 0), coalesce(mr.metrics_json, '{}')
from match_slots ms
left join match_results mr on mr.match_id = ms.match_id and mr.team_id = ms.team_id
where ms.match_id = ?
order by ms.slot_index`, []any{m.id}, func(rows *sql.Rows) (slotRes, error) {
			var s slotRes
			return s, rows.Scan(&s.teamID, &s.place, &s.total, &s.plus, &s.metrics)
		})
		if err != nil {
			return nil, false, err
		}
		outcome := structure.MatchOutcome{Code: m.code, Finished: finished}
		for _, s := range slots {
			metrics := map[string]float64{"total": s.total, "plus": s.plus}
			var parsed map[string]any
			if json.Unmarshal([]byte(s.metrics), &parsed) == nil {
				for key, value := range parsed {
					if n, ok := value.(float64); ok {
						metrics[key] = n
					}
				}
			}
			outcome.Slots = append(outcome.Slots, structure.SlotOutcome{
				Participant: s.teamID,
				Place:       s.place,
				Metrics:     metrics,
			})
		}
		outcomes = append(outcomes, outcome)
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
select ms.id, ms.match_id, ms.source_type, ms.source_ref_json, coalesce(ms.team_id, 0)
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

	var affected []int64
	for _, slot := range slots {
		var ref map[string]any
		_ = json.Unmarshal([]byte(slot.sourceRef), &ref)

		var desired int64
		switch slot.sourceType {
		case "from_match":
			desired, err = teamAtMatchPlace(ctx, tx, gameID, store.StringFromMap(ref, "match"), store.IntFromMap(ref, "place"))
		case "reseed":
			desired, err = teamAtReseedRank(ctx, tx, gameID, store.StringFromMap(ref, "stage"), store.IntFromMap(ref, "rank"))
		}
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

// teamAtMatchPlace returns the team that took the given place in a bout, but
// only once that bout is finished — provisional standings must not leak
// downstream. Returns 0 when unresolved.
func teamAtMatchPlace(ctx context.Context, q store.Queryer, gameID int64, matchCode string, place int) (int64, error) {
	if matchCode == "" || place <= 0 {
		return 0, nil
	}
	var teamID int64
	err := q.QueryRowContext(ctx, `
select mr.team_id
from match_results mr
join matches m on m.id = mr.match_id
where m.game_id = ? and m.code = ? and m.status = 'finished' and mr.place = ?`,
		gameID, matchCode, float64(place)).Scan(&teamID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return teamID, err
}

// teamAtReseedRank returns the team at a reseed rank, or 0 when the reseed has
// not been computed yet.
func teamAtReseedRank(ctx context.Context, q store.Queryer, gameID int64, stageCode string, rank int) (int64, error) {
	if stageCode == "" || rank <= 0 {
		return 0, nil
	}
	// Kind-ranked stages (rr groups, brackets) recompute standings live, so a
	// rank ref must not leak provisional order downstream: it resolves only
	// once every bout of the source stage is finished. Reseed stages keep
	// their explicit-calculate gate (standings exist only when calculated).
	var stageID int64
	var kind string
	err := q.QueryRowContext(ctx, `
select id, kind from stages where game_id = ? and code = ?`, gameID, stageCode).Scan(&stageID, &kind)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if isRankedKind(kind) {
		var unfinished int
		if err := q.QueryRowContext(ctx, `
select count(*) from matches where stage_id = ? and status != 'finished'`, stageID).Scan(&unfinished); err != nil {
			return 0, err
		}
		if unfinished > 0 {
			return 0, nil
		}
	}
	var teamID int64
	err = q.QueryRowContext(ctx, `
select participant_id from stage_standings where stage_id = ? and rank = ?`,
		stageID, rank).Scan(&teamID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return teamID, err
}

// applyResolvedSlotTx writes a slot's resolved occupant and reports whether it
// actually changed (so the caller can collect the affected match for broadcast).
//
// It is non-destructive: it never deletes a slot's protocol data (themes /
// answers / results). Two cases matter:
//
//   - desired == 0: the upstream source is not currently final — e.g. a finished
//     bout was unticked so it could be edited. We HOLD the current occupant and
//     its data instead of flushing it; re-finishing the source restores the same
//     slot with no churn, so untick→edit→retick loses nothing. (Genuine occupant
//     changes still flow through, because those have desired != 0.)
//   - desired != 0 and differs from current: a different team now occupies the
//     slot. We move the occupant and reopen the bout (status='active') so its
//     standings get re-reviewed against the new team — but we leave the previous
//     occupant's rows in place rather than deleting them.
func applyResolvedSlotTx(ctx context.Context, tx *sql.Tx, slotID, matchID, current, desired int64, gameType string) (bool, error) {
	if desired == current {
		return false, nil
	}
	if desired == 0 {
		// Source temporarily unresolved (mid-edit). Hold, don't flush.
		return false, nil
	}
	if current != 0 {
		// A genuinely different team now occupies this slot. Reopen the bout so
		// its standings are re-reviewed; the previous occupant's protocol stays
		// in the DB (non-destructive — recoverable, never silently deleted).
		if _, err := tx.ExecContext(ctx, `update matches set status = 'active' where id = ? and status = 'finished'`, matchID); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `update match_slots set team_id = ? where id = ?`, nullableInt64(desired), slotID); err != nil {
		return false, err
	}
	return true, nil
}

// --- reseed computation --------------------------------------------------

type reseedConfig struct {
	Teams   []store.SchemeSlot `json:"teams"`
	Sources []string           `json:"sources"`
	Sort    []reseedSortRule   `json:"sort"`
}

type reseedSortRule struct {
	Metric string `json:"metric"`
	Dir    string `json:"dir"`
}

// reseed metric keys persisted per entry, beyond the place_sum/total/plus sums.
var reseedCountMetrics = []string{"correct_50", "correct_40", "correct_30", "correct_20"}

type reseedEntry struct {
	teamID  int64
	metrics map[string]float64
	bouts   []string
}

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

type reseedPrerequisiteState struct {
	Ready          bool
	SourceMatchIDs []int64
	PendingMatches []string
}

// ReseedPrerequisites reports whether a reseed stage's source bouts are all
// finished, listing the source bout ids and the codes of any still-pending ones.
func ReseedPrerequisites(ctx context.Context, q store.Queryer, config []byte, gameID int64) (reseedPrerequisiteState, error) {
	var state reseedPrerequisiteState
	var cfg reseedConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return state, err
	}

	sourceMatchIDs, sourcePending, err := reseedSourceMatches(ctx, q, gameID, cfg)
	if err != nil {
		return state, err
	}
	state.SourceMatchIDs = sourceMatchIDs
	for _, code := range sourcePending {
		state.addPending(code)
	}

	advancing := 0
	for _, slot := range cfg.Teams {
		if slot.FromMatch == nil {
			continue
		}
		teamID, err := teamAtMatchPlace(ctx, q, gameID, slot.FromMatch.Match, slot.FromMatch.Place)
		if err != nil {
			return state, err
		}
		if teamID == 0 {
			state.addPending(slot.FromMatch.Match)
		}
		advancing++
	}
	if advancing == 0 {
		return state, nil
	}
	state.Ready = len(state.SourceMatchIDs) > 0 && len(state.PendingMatches) == 0
	return state, nil
}

func (state *reseedPrerequisiteState) addPending(code string) {
	code = strings.TrimSpace(code)
	if code == "" {
		return
	}
	for _, existing := range state.PendingMatches {
		if existing == code {
			return
		}
	}
	state.PendingMatches = append(state.PendingMatches, code)
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

// recomputeReseedEntriesTx rebuilds a reseed stage's entries from match
// results. Metrics (place_sum, total, plus, correct_*) are summed across every
// bout each advancing team played in the stages listed under config `sources`
// (e.g. both the 1/16 and the 1/8). If `sources` is absent it falls back to the
// single bout each team advanced from. Entries are cleared until every source
// bout is finished, so downstream reseed slots stay unresolved until then.
func recomputeReseedEntriesTx(ctx context.Context, tx *sql.Tx, stageID int64, config []byte, gameID int64) error {
	var cfg reseedConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return err
	}

	clear := func() error {
		_, err := tx.ExecContext(ctx, `delete from stage_standings where stage_id = ?`, stageID)
		return err
	}

	// Advancing teams come from the fromMatch place selectors in `teams`.
	advancing := make([]int64, 0, len(cfg.Teams))
	for _, slot := range cfg.Teams {
		if slot.FromMatch == nil {
			continue
		}
		teamID, err := teamAtMatchPlace(ctx, tx, gameID, slot.FromMatch.Match, slot.FromMatch.Place)
		if err != nil {
			return err
		}
		if teamID == 0 {
			return clear() // a source bout is not finished yet
		}
		advancing = append(advancing, teamID)
	}
	if len(advancing) == 0 {
		return clear()
	}

	// Source bouts whose results are summed. Either the listed source stages or
	// (fallback) just the bouts named in `teams`.
	state, err := ReseedPrerequisites(ctx, tx, config, gameID)
	if err != nil {
		return err
	}
	if !state.Ready {
		return clear()
	}

	entries := make([]reseedEntry, 0, len(advancing))
	for _, teamID := range advancing {
		entry, err := aggregateReseedMetrics(ctx, tx, teamID, state.SourceMatchIDs)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
	}

	rules := cfg.Sort
	if len(rules) == 0 {
		rules = []reseedSortRule{{Metric: "place_sum", Dir: "asc"}}
	}

	// Жребий lots are derived deterministically from the game's fixed random
	// seed, so a tie always breaks the same way no matter how many times the
	// reseed is recomputed — re-finishing an edited source bout can never
	// reshuffle the lottery.
	seed, err := gameRandomSeed(ctx, tx, gameID)
	if err != nil {
		return err
	}

	sortReseedEntries(entries, rules)
	assignDrawLots(entries, rules, seed)
	sortReseedEntries(entries, rules) // re-order now that tied groups have lots

	if err := clear(); err != nil {
		return err
	}
	for rank, entry := range entries {
		out := map[string]any{
			"place_sum": entry.metrics["place_sum"],
			"total":     int(entry.metrics["total"]),
			"plus":      int(entry.metrics["plus"]),
			"draw":      int(entry.metrics["draw"]),
			"match":     strings.Join(entry.bouts, "+"),
		}
		for _, key := range reseedCountMetrics {
			out[key] = int(entry.metrics[key])
		}
		if _, err := tx.ExecContext(ctx, `
insert into stage_standings(stage_id, rank, participant_id, metrics_json)
values(?, ?, ?, ?)`, stageID, rank+1, entry.teamID, util.MustJSON(out)); err != nil {
			return err
		}
	}
	return nil
}

// sortReseedEntries orders entries by the configured sort rules, with team id
// as a final deterministic tiebreak.
func sortReseedEntries(entries []reseedEntry, rules []reseedSortRule) {
	sort.SliceStable(entries, func(i, j int) bool {
		for _, rule := range rules {
			a, b := entries[i].metrics[rule.Metric], entries[j].metrics[rule.Metric]
			if a == b {
				continue
			}
			if rule.Dir == "desc" {
				return a > b
			}
			return a < b
		}
		return entries[i].teamID < entries[j].teamID
	})
}

// tiedOnEveryMetricButDraw reports whether two entries are equal on every sort
// metric except the lottery (draw) — i.e. only Жребий can separate them.
func tiedOnEveryMetricButDraw(a, b reseedEntry, rules []reseedSortRule) bool {
	for _, rule := range rules {
		if rule.Metric == "draw" {
			continue
		}
		if a.metrics[rule.Metric] != b.metrics[rule.Metric] {
			return false
		}
	}
	return true
}

// assignDrawLots gives every team in a true tie group a Жребий lot derived
// deterministically from the game's fixed random seed, so the lottery order is
// stable across recomputes (untick/retick or an unrelated score edit can never
// reshuffle a tie). Untied teams keep draw 0.
func assignDrawLots(entries []reseedEntry, rules []reseedSortRule, seed string) {
	i := 0
	for i < len(entries) {
		j := i + 1
		for j < len(entries) && tiedOnEveryMetricButDraw(entries[i], entries[j], rules) {
			j++
		}
		if j-i >= 2 {
			for k := i; k < j; k++ {
				entries[k].metrics["draw"] = float64(deterministicLot(seed, entries[k].teamID))
			}
		}
		i = j
	}
}

// deterministicLot derives a stable Жребий lot in [1, 1_000_000] for a team from
// the game's fixed random seed. Same (seed, team) always yields the same lot, so
// a reseed recomputes identically every time. A hash collision inside a tie group
// is harmless: sortReseedEntries breaks any residual tie by team id.
func deterministicLot(seed string, teamID int64) int64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%s:%d", seed, teamID)
	return int64(h.Sum64()%1_000_000) + 1
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

// reseedSourceMatches returns the bout ids that contribute to a reseed and the
// codes of source bouts that are not finished yet.
func reseedSourceMatches(ctx context.Context, q store.Queryer, gameID int64, cfg reseedConfig) ([]int64, []string, error) {
	var rows []resolverBout
	var err error
	if len(cfg.Sources) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(cfg.Sources)), ",")
		args := []any{gameID}
		for _, code := range cfg.Sources {
			args = append(args, code)
		}
		rows, err = store.CollectRows(ctx, q, fmt.Sprintf(`
select m.id, m.code, m.status from matches m
join stages s on s.id = m.stage_id
where m.game_id = ? and s.code in (%s)
order by s.position, m.position, m.id`, placeholders), args, scanResolverBout)
	} else {
		codes := make(map[string]struct{})
		for _, slot := range cfg.Teams {
			if slot.FromMatch != nil {
				codes[slot.FromMatch.Match] = struct{}{}
			}
		}
		if len(codes) == 0 {
			return nil, nil, nil
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
		rows, err = store.CollectRows(ctx, q, fmt.Sprintf(`
select id, code, status from matches where game_id = ? and code in (%s)
order by position, id`, placeholders), args, scanResolverBout)
	}
	if err != nil {
		return nil, nil, err
	}
	ids := make([]int64, 0, len(rows))
	pending := make([]string, 0)
	for _, r := range rows {
		ids = append(ids, r.id)
		if r.status != "finished" {
			pending = append(pending, r.code)
		}
	}
	return ids, pending, nil
}

type resolverBout struct {
	id     int64
	code   string
	status string
}

func scanResolverBout(rows *sql.Rows) (resolverBout, error) {
	var b resolverBout
	return b, rows.Scan(&b.id, &b.code, &b.status)
}

// numFromAny reads a JSON number decoded into an interface{} (always float64).
func numFromAny(value any) float64 {
	if n, ok := value.(float64); ok {
		return n
	}
	return 0
}

// aggregateReseedMetrics sums one team's place/total/plus/correct_* across the
// given source bouts.
func aggregateReseedMetrics(ctx context.Context, q store.Queryer, teamID int64, sourceMatchIDs []int64) (reseedEntry, error) {
	entry := reseedEntry{teamID: teamID, metrics: map[string]float64{}}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(sourceMatchIDs)), ",")
	args := []any{teamID}
	for _, id := range sourceMatchIDs {
		args = append(args, id)
	}
	rows, err := q.QueryContext(ctx, fmt.Sprintf(`
select m.code, mr.place, mr.total, mr.plus, mr.metrics_json
from match_results mr
join matches m on m.id = mr.match_id
join stages s on s.id = m.stage_id
where mr.team_id = ? and mr.match_id in (%s)
order by s.position, m.position, m.id`, placeholders), args...)
	if err != nil {
		return entry, err
	}
	defer rows.Close()
	for rows.Next() {
		var code, rawMetrics string
		var place float64
		var total, plus int
		if err := rows.Scan(&code, &place, &total, &plus, &rawMetrics); err != nil {
			return entry, err
		}
		entry.bouts = append(entry.bouts, code)
		entry.metrics["place_sum"] += place
		entry.metrics["total"] += float64(total)
		entry.metrics["plus"] += float64(plus)
		// metrics_json mixes scalars (correct_50, draw, ...) with arrays
		// (correctCounts, wrongCounts), so decode into map[string]any and pull
		// just the scalar keys we sum — decoding into map[string]float64 would
		// fail on the arrays and silently drop every count.
		var parsed map[string]any
		if err := json.Unmarshal([]byte(rawMetrics), &parsed); err == nil {
			for _, key := range reseedCountMetrics {
				entry.metrics[key] += numFromAny(parsed[key])
			}
		}
	}
	return entry, rows.Err()
}
