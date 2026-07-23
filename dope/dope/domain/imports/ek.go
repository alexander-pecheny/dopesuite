package imports

import (
	"context"
	"database/sql"
	"dope/dope/domain/core"
	"dope/dope/domain/resolver"
	"dope/dope/domain/scoring"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
)

// ek.go is a one-shot maintenance entrypoint
// (`dope import-ek-results --db ... --plan ek_plan.json [--apply]`) used to
// restore the СтудЧР-2026 ЭК bracket after it was played in a Google Sheet
// while only the start of the game was saved in the app. It replays the parsed
// sheet through the app's own write path — per match it writes the per-theme
// player, the five per-question marks, and the manual place, marks the bout
// finished, recalculates results, and resolves downstream slots (and the reseed
// stage) exactly as the live editor would. Per-row audit capture is suppressed
// (bulk restore; rely on the external pre-restore backup for rollback).
//
// Without --apply it runs the whole thing in a transaction and rolls back,
// printing the resulting standings — a dry run against the real data.

type ekPlanTheme struct {
	ThemeIndex int      `json:"theme_index"`
	PlayerID   *int64   `json:"player_id"`
	Marks      []string `json:"marks"`
}

type ekPlanTeam struct {
	TeamID int64         `json:"team_id"`
	Place  *float64      `json:"place"`
	Themes []ekPlanTheme `json:"themes"`
}

type ekPlanMatch struct {
	Teams []ekPlanTeam `json:"teams"`
}

type ekPlan struct {
	FestID  int64                  `json:"fest_id"`
	GameID  int64                  `json:"game_id"`
	Order   []string               `json:"order"`
	Matches map[string]ekPlanMatch `json:"matches"`
}

// RunEKImport is the CLI entrypoint. openDB opens (and migrates) the fest
// database; it is injected by the caller so this package stays free of the
// schema-migration / journal-trigger setup that lives in the server package.
func RunEKImport(args []string, openDB func(string) (*sql.DB, error)) {
	fs := flag.NewFlagSet("import-ek-results", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to the sqlite database")
	planPath := fs.String("plan", "", "path to ek_plan.json")
	apply := fs.Bool("apply", false, "commit changes (default is dry-run rollback)")
	_ = fs.Parse(args)
	if *dbPath == "" || *planPath == "" {
		log.Fatal("import-ek-results: --db and --plan are required")
	}

	raw, err := os.ReadFile(*planPath)
	if err != nil {
		log.Fatalf("read plan: %v", err)
	}
	var plan ekPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		log.Fatalf("parse plan: %v", err)
	}

	db, err := openDB(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	eng := core.Engine{DB: db}
	ctx := context.Background()
	tx, err := eng.BeginWriteTx(ctx)
	if err != nil {
		log.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	// Bulk restore: skip per-row audit capture (we keep an external backup).
	if err := festwrite.SuppressAuditTx(ctx, tx); err != nil {
		log.Fatalf("suppress audit: %v", err)
	}

	for _, code := range plan.Order {
		if len(code) > 8 && code[0] == '@' {
			// "@reseed:<stageCode>"
			stageCode := code[len("@reseed:"):]
			log.Printf("reseed: calculating stage %q", stageCode)
			if _, err := resolver.CalculateReseedStageSlotsTx(ctx, tx, plan.GameID, stageCode); err != nil {
				log.Fatalf("reseed %s: %v", stageCode, err)
			}
			continue
		}
		if err := importEKMatch(ctx, tx, plan, code); err != nil {
			log.Fatalf("match %s: %v", code, err)
		}
	}

	printStandings(ctx, tx, plan.GameID)

	if *apply {
		if err := tx.Commit(); err != nil {
			log.Fatalf("commit: %v", err)
		}
		log.Printf("APPLIED: EK results imported into game %d", plan.GameID)
	} else {
		log.Printf("DRY RUN: rolled back (pass --apply to commit)")
	}
}

func importEKMatch(ctx context.Context, tx *sql.Tx, plan ekPlan, code string) error {
	pm, ok := plan.Matches[code]
	if !ok {
		return fmt.Errorf("no plan for match %s", code)
	}
	match, err := store.LoadDBMatchState(ctx, tx, plan.FestID, code)
	if err != nil {
		return fmt.Errorf("load match: %w", err)
	}

	// Map team_id -> slot index from the (already resolved) match slots, and
	// verify the plan's teams are exactly the bout's occupants. A mismatch means
	// the app advanced a different team than the sheet did — abort loudly.
	slotOf := map[int64]int{}
	for i, id := range match.TeamIDs {
		if id != 0 {
			slotOf[id] = i
		}
	}
	planIDs := map[int64]bool{}
	for _, t := range pm.Teams {
		planIDs[t.TeamID] = true
		if _, ok := slotOf[t.TeamID]; !ok {
			return fmt.Errorf("plan team %d not present in bout %s slots %v", t.TeamID, code, match.TeamIDs)
		}
	}
	for _, id := range match.TeamIDs {
		if id != 0 && !planIDs[id] {
			return fmt.Errorf("bout %s has team %d not in plan", code, id)
		}
	}

	for _, t := range pm.Teams {
		if t.Place != nil {
			if _, err := tx.ExecContext(ctx, `
insert into match_results(match_id, team_id, place) values(?, ?, ?)
on conflict(match_id, team_id) do update set place = excluded.place`,
				match.MatchID, t.TeamID, *t.Place); err != nil {
				return err
			}
		}
	}
	if err := festwrite.MutateMatchBlobTx(ctx, tx, match.MatchID, func(blob *store.MatchBlob) error {
		for _, t := range pm.Teams {
			for _, th := range t.Themes {
				if th.PlayerID != nil {
					blob.SetPlayer(t.TeamID, "regular", th.ThemeIndex, *th.PlayerID)
				}
				for ai, mark := range th.Marks {
					blob.SetAnswer(t.TeamID, "regular", th.ThemeIndex, ai, mark)
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// Mark finished directly so the manual places (above) survive — the live
	// "finished" path would overwrite them with computed places.
	if _, err := tx.ExecContext(ctx, `update matches set status = 'finished' where id = ?`, match.MatchID); err != nil {
		return err
	}

	// Reload so the in-memory state reflects the written answers/place/finished,
	// then recalc results and propagate to downstream slots.
	match, err = store.LoadDBMatchState(ctx, tx, plan.FestID, code)
	if err != nil {
		return err
	}
	if err := scoring.RecalculateMatchResultsTx(ctx, tx, match); err != nil {
		return err
	}
	if _, err := resolver.ResolveGameSlotsTx(ctx, tx, plan.GameID); err != nil {
		return err
	}
	log.Printf("match %s: imported %d teams", code, len(pm.Teams))
	return nil
}

func printStandings(ctx context.Context, tx *sql.Tx, gameID int64) {
	rows, err := tx.QueryContext(ctx, `
select m.code, t.name, r.place, r.total, r.plus, m.status
from match_results r
join matches m on m.id = r.match_id
join teams t on t.id = r.team_id
where m.game_id = ?
order by m.position, m.code, r.place`, gameID)
	if err != nil {
		log.Printf("standings query: %v", err)
		return
	}
	defer rows.Close()
	fmt.Println("\n==== EK standings after import ====")
	cur := ""
	for rows.Next() {
		var mcode, name, status string
		var place float64
		var total, plus int
		if err := rows.Scan(&mcode, &name, &place, &total, &plus, &status); err != nil {
			log.Printf("scan: %v", err)
			return
		}
		if mcode != cur {
			fmt.Printf("-- Бой %s [%s]\n", mcode, status)
			cur = mcode
		}
		fmt.Printf("   %.0f. %-30s Σ=%-5d Σ+=%-5d\n", place, name, total, plus)
	}
}
