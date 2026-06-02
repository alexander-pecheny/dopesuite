package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"sort"
	"strings"
)

// runResolveBracket is a maintenance entrypoint (`dope resolve-bracket --db ...
// --game N`) that re-runs slot resolution for one game's bracket. It applies
// the same logic the live write path uses, so it reconciles reseed_entries and
// downstream slots after manual data edits, imports, or migrations.
func runResolveBracket(args []string) {
	fs := flag.NewFlagSet("resolve-bracket", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to the sqlite database")
	gameID := fs.Int64("game", 0, "game id to reconcile")
	_ = fs.Parse(args)
	if *dbPath == "" || *gameID == 0 {
		log.Fatal("resolve-bracket: --db and --game are required")
	}

	db, err := openFestDB(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	srv := &server{db: db}
	ctx := context.Background()
	tx, err := srv.beginWriteTx(ctx)
	if err != nil {
		log.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()
	if _, err := resolveGameSlotsTx(ctx, tx, *gameID); err != nil {
		log.Fatalf("resolve game %d: %v", *gameID, err)
	}
	if err := tx.Commit(); err != nil {
		log.Fatalf("commit: %v", err)
	}
	log.Printf("resolve-bracket: reconciled game %d in %s", *gameID, *dbPath)
}

// resolveGameSlotsTx propagates results forward through a game's bracket. It
// runs inside the write transaction after a match's results are recalculated,
// and is responsible for the parts of the bracket that depend on other matches:
//
//   - reseed stages: recompute reseed_entries from the results of their source
//     bouts (summed across every game each advancing team played);
//   - from_match / reseed slots: fill match_slots.team_id once the upstream
//     source is final, and (for EK) create that team's themes.
//
// It is idempotent: a slot is only rewritten when its resolved occupant
// actually changes. When an occupant changes the previously assigned team's
// themes/answers/results in that bout are dropped and the bout is reopened, so
// a single forward pass (stages in position order) also invalidates anything
// further downstream.
// resolveGameSlotsTx resolves every from_match/reseed slot in the game and
// returns the ids of matches whose slots actually changed — so a caller can
// broadcast those downstream matches (a finished bout advances teams into the
// next round, which would otherwise only show up on a viewer reload).
func resolveGameSlotsTx(ctx context.Context, tx *sql.Tx, gameID int64) ([]int64, error) {
	var gameType string
	if err := tx.QueryRowContext(ctx, `select game_type from games where id = ?`, gameID).Scan(&gameType); err != nil {
		return nil, err
	}

	stages, err := collectRows(ctx, tx, `
select id, code, stage_type, config_json
from stages where game_id = ? order by position, id`,
		[]any{gameID}, func(rows *sql.Rows) (resolverStage, error) {
			var st resolverStage
			return st, rows.Scan(&st.id, &st.code, &st.stageType, &st.config)
		})
	if err != nil {
		return nil, err
	}

	var affected []int64
	seen := map[int64]bool{}
	for _, stage := range stages {
		if stage.stageType == "reseed" {
			if err := recomputeReseedEntriesTx(ctx, tx, stage.id, stage.config, gameID); err != nil {
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
	return affected, nil
}

type resolverStage struct {
	id        int64
	code      string
	stageType string
	config    []byte
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
	slots, err := collectRows(ctx, tx, `
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
			desired, err = teamAtMatchPlace(ctx, tx, gameID, stringFromMap(ref, "match"), intFromMap(ref, "place"))
		case "reseed":
			desired, err = teamAtReseedRank(ctx, tx, gameID, stringFromMap(ref, "stage"), intFromMap(ref, "rank"))
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
func teamAtMatchPlace(ctx context.Context, tx *sql.Tx, gameID int64, matchCode string, place int) (int64, error) {
	if matchCode == "" || place <= 0 {
		return 0, nil
	}
	var teamID int64
	err := tx.QueryRowContext(ctx, `
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
func teamAtReseedRank(ctx context.Context, tx *sql.Tx, gameID int64, stageCode string, rank int) (int64, error) {
	if stageCode == "" || rank <= 0 {
		return 0, nil
	}
	var teamID int64
	err := tx.QueryRowContext(ctx, `
select re.team_id
from reseed_entries re
join stages s on s.id = re.stage_id
where s.game_id = ? and s.code = ? and re.rank = ?`,
		gameID, stageCode, rank).Scan(&teamID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return teamID, err
}

// applyResolvedSlotTx writes a slot's resolved team when it changed. Replacing
// an existing occupant drops that team's data in the bout and reopens it.
// applyResolvedSlotTx writes a slot's resolved occupant and reports whether it
// actually changed (so the caller can collect the affected match for broadcast).
func applyResolvedSlotTx(ctx context.Context, tx *sql.Tx, slotID, matchID, current, desired int64, gameType string) (bool, error) {
	if desired == current {
		return false, nil
	}
	if current != 0 {
		// The previous occupant's protocol and standing in this bout are no
		// longer valid; drop them (answers cascade from themes) and reopen the
		// bout so its results — and anything downstream — get recomputed.
		if _, err := tx.ExecContext(ctx, `delete from themes where match_id = ? and team_id = ?`, matchID, current); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `delete from match_results where match_id = ? and team_id = ?`, matchID, current); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `update matches set status = 'active' where id = ? and status = 'finished'`, matchID); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `update match_slots set team_id = ? where id = ?`, nullableInt64(desired), slotID); err != nil {
		return false, err
	}
	if desired != 0 && gameType == "ek" {
		if err := ensureRegularThemes(ctx, tx, matchID, desired); err != nil {
			return false, err
		}
	}
	return true, nil
}

// --- reseed computation --------------------------------------------------

type reseedConfig struct {
	Teams   []schemeSlot     `json:"teams"`
	Sources []string         `json:"sources"`
	Sort    []reseedSortRule `json:"sort"`
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
		_, err := tx.ExecContext(ctx, `delete from reseed_entries where stage_id = ?`, stageID)
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
	sourceMatchIDs, allFinished, err := reseedSourceMatches(ctx, tx, gameID, cfg)
	if err != nil {
		return err
	}
	if len(sourceMatchIDs) == 0 || !allFinished {
		return clear()
	}

	entries := make([]reseedEntry, 0, len(advancing))
	for _, teamID := range advancing {
		entry, err := aggregateReseedMetrics(ctx, tx, teamID, sourceMatchIDs)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
	}

	rules := cfg.Sort
	if len(rules) == 0 {
		rules = []reseedSortRule{{Metric: "place_sum", Dir: "asc"}}
	}

	// Persisted lots (Жребий) for teams that were tied on a previous recompute,
	// so the lottery order stays stable as unrelated scores change.
	prevDraw, err := loadReseedDraws(ctx, tx, stageID)
	if err != nil {
		return err
	}

	sortReseedEntries(entries, rules)
	assignDrawLots(entries, rules, prevDraw)
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
insert into reseed_entries(stage_id, rank, team_id, metrics_json)
values(?, ?, ?, ?)`, stageID, rank+1, entry.teamID, mustJSON(out)); err != nil {
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

// assignDrawLots gives every team in a true tie group a Жребий lot. Lots already
// drawn on a prior recompute are reused (so the order is stable); only teams
// without one draw a fresh, distinct lot. Untied teams keep draw 0.
func assignDrawLots(entries []reseedEntry, rules []reseedSortRule, prevDraw map[int64]float64) {
	i := 0
	for i < len(entries) {
		j := i + 1
		for j < len(entries) && tiedOnEveryMetricButDraw(entries[i], entries[j], rules) {
			j++
		}
		if j-i >= 2 {
			used := map[int64]bool{}
			for k := i; k < j; k++ {
				if lot := prevDraw[entries[k].teamID]; lot != 0 {
					entries[k].metrics["draw"] = lot
					used[int64(lot)] = true
				}
			}
			for k := i; k < j; k++ {
				if entries[k].metrics["draw"] == 0 {
					lot := freshLot(used)
					entries[k].metrics["draw"] = float64(lot)
					used[lot] = true
				}
			}
		}
		i = j
	}
}

// freshLot returns a positive lot value not already used in its tie group.
func freshLot(used map[int64]bool) int64 {
	for {
		lot := rand.Int64N(1_000_000) + 1
		if !used[lot] {
			return lot
		}
	}
}

// loadReseedDraws reads the current draw (Жребий) lot per team for a stage, so
// previously drawn lots survive a recompute.
func loadReseedDraws(ctx context.Context, tx *sql.Tx, stageID int64) (map[int64]float64, error) {
	draws := map[int64]float64{}
	rows, err := tx.QueryContext(ctx, `select team_id, metrics_json from reseed_entries where stage_id = ?`, stageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var teamID int64
		var raw string
		if err := rows.Scan(&teamID, &raw); err != nil {
			return nil, err
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			if lot := numFromAny(parsed["draw"]); lot != 0 {
				draws[teamID] = lot
			}
		}
	}
	return draws, rows.Err()
}

// reseedSourceMatches returns the bout ids that contribute to a reseed and
// whether all of them are finished.
func reseedSourceMatches(ctx context.Context, tx *sql.Tx, gameID int64, cfg reseedConfig) ([]int64, bool, error) {
	var rows []resolverBout
	var err error
	if len(cfg.Sources) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(cfg.Sources)), ",")
		args := []any{gameID}
		for _, code := range cfg.Sources {
			args = append(args, code)
		}
		rows, err = collectRows(ctx, tx, fmt.Sprintf(`
select m.id, m.status from matches m
join stages s on s.id = m.stage_id
where m.game_id = ? and s.code in (%s)`, placeholders), args, scanResolverBout)
	} else {
		codes := make(map[string]struct{})
		for _, slot := range cfg.Teams {
			if slot.FromMatch != nil {
				codes[slot.FromMatch.Match] = struct{}{}
			}
		}
		if len(codes) == 0 {
			return nil, false, nil
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(codes)), ",")
		args := []any{gameID}
		for code := range codes {
			args = append(args, code)
		}
		rows, err = collectRows(ctx, tx, fmt.Sprintf(`
select id, status from matches where game_id = ? and code in (%s)`, placeholders), args, scanResolverBout)
	}
	if err != nil {
		return nil, false, err
	}
	ids := make([]int64, 0, len(rows))
	allFinished := true
	for _, r := range rows {
		ids = append(ids, r.id)
		if r.status != "finished" {
			allFinished = false
		}
	}
	return ids, allFinished, nil
}

type resolverBout struct {
	id     int64
	status string
}

func scanResolverBout(rows *sql.Rows) (resolverBout, error) {
	var b resolverBout
	return b, rows.Scan(&b.id, &b.status)
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
func aggregateReseedMetrics(ctx context.Context, tx *sql.Tx, teamID int64, sourceMatchIDs []int64) (reseedEntry, error) {
	entry := reseedEntry{teamID: teamID, metrics: map[string]float64{}}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(sourceMatchIDs)), ",")
	args := []any{teamID}
	for _, id := range sourceMatchIDs {
		args = append(args, id)
	}
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
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
