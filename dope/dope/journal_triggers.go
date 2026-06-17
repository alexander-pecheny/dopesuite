package main

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
)

// Journal row-op triggers capture the FORWARD row delta of every fine edit to a
// game-scoped table directly into the unified journal (op + JSON payload),
// stamping the owning game_id (derived from the row — safe because fine edits
// never delete their parent, and bulk/cascade ops are suppressed and instead
// bracketed by checkpoints). This is the deterministic, replayable backbone for
// per-game derived revert. Forward-only: INSERT stores the full row, UPDATE the
// changed columns, DELETE just the primary key.
//
// These coexist with the legacy audit_log triggers during the transition; the
// cutover removes audit_log and its triggers once revert reads the journal.

const journalTriggerTemplateVersion = 2

// journalGameTables are the audited tables a game owns, with the SQL expression
// that derives the owning game_id from a NEW/OLD row.
var journalGameTables = []struct {
	table   string
	gameVia func(prefix string) string // returns a game_id expression for new./old.
}{
	{"games", func(p string) string { return p + ".id" }},
	{"stages", func(p string) string { return p + ".game_id" }},
	{"matches", func(p string) string { return p + ".game_id" }},
	{"match_slots", func(p string) string {
		return fmt.Sprintf("(select game_id from matches where id = %s.match_id)", p)
	}},
	{"themes", func(p string) string {
		return fmt.Sprintf("(select game_id from matches where id = %s.match_id)", p)
	}},
	{"answers", func(p string) string {
		return fmt.Sprintf("(select m.game_id from matches m join themes th on th.match_id = m.id where th.id = %s.theme_id)", p)
	}},
	{"match_results", func(p string) string {
		return fmt.Sprintf("(select game_id from matches where id = %s.match_id)", p)
	}},
	{"reseed_entries", func(p string) string {
		return fmt.Sprintf("(select game_id from stages where id = %s.stage_id)", p)
	}},
	{"game_assignments", func(p string) string { return p + ".game_id" }},
	{"game_teams", func(p string) string { return p + ".game_id" }},
	{"game_players", func(p string) string { return p + ".game_id" }},
	{"game_team_players", func(p string) string { return p + ".game_id" }},
	{"game_player_team_overrides", func(p string) string { return p + ".game_id" }},
}

func isJournalGameTable(name string) bool {
	for _, t := range journalGameTables {
		if t.table == name {
			return true
		}
	}
	return false
}

// buildJournalRowTrigger emits an AFTER trigger that records a forward row-op.
func buildJournalRowTrigger(table string, cols, pks []string, op string, gameVia func(string) string) string {
	if len(pks) == 0 {
		pks = []string{"rowid"}
	}
	const now = `strftime('%Y-%m-%dT%H:%M:%fZ','now')`
	const actorSel = `(select actor_user_id from audit_ctx where id = 1)`
	const reqSel = `(select request_id from audit_ctx where id = 1)`
	// Unlike the legacy audit_log triggers, the journal captures EVERY row change
	// including bulk/structural churn (imports, reseeds). That keeps the forward
	// log complete so replay can cross any operation from a single genesis
	// checkpoint — no per-structural-op checkpoint barriers needed. The volume is
	// reclaimed by the archiver (zstd over the cold segment ~95%, as the audit
	// converter measured), so completeness costs almost nothing on disk.

	rowJSON := func(prefix string) string {
		parts := make([]string, 0, len(cols))
		for _, c := range cols {
			parts = append(parts, fmt.Sprintf("'%s', %s.%s", c, prefix, quoteIdent(c)))
		}
		return "json_object(" + strings.Join(parts, ", ") + ")"
	}
	pkJSON := func(prefix string) string {
		parts := make([]string, 0, len(pks))
		for _, c := range pks {
			parts = append(parts, fmt.Sprintf("'%s', %s.%s", c, prefix, quoteIdent(c)))
		}
		return "json_object(" + strings.Join(parts, ", ") + ")"
	}
	// changedRowJSON keeps PK + changed columns only (forward UPDATE delta).
	jsonPathLit := func(c string) string { return "'$.\"" + strings.ReplaceAll(c, `"`, `""`) + "\"'" }
	changedRowJSON := func(prefix string) string {
		pkSet := map[string]bool{}
		for _, p := range pks {
			pkSet[p] = true
		}
		removals := make([]string, 0, len(cols))
		for _, c := range cols {
			if pkSet[c] {
				continue
			}
			removals = append(removals, fmt.Sprintf("case when old.%s is new.%s then %s else '$.\"__dope_keep__\"' end",
				quoteIdent(c), quoteIdent(c), jsonPathLit(c)))
		}
		if len(removals) == 0 {
			return rowJSON(prefix)
		}
		return "json_remove(" + rowJSON(prefix) + ", " + strings.Join(removals, ", ") + ")"
	}

	payload := func(table, rowExpr string) string {
		return fmt.Sprintf("json_object('t', '%s', 'r', %s)", table, rowExpr)
	}
	festOf := func(gameExpr string) string {
		return fmt.Sprintf("(select fest_id from games where id = %s)", gameExpr)
	}
	seqOf := func(festExpr string) string {
		return fmt.Sprintf("coalesce((select revision from fests where id = %s), 0)", festExpr)
	}
	insertSelect := func(gameExpr, opCode, rowExpr, prefixForRow string) string {
		fest := festOf(gameExpr)
		return fmt.Sprintf(
			`insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select %s, %s, %s, %s, %s, %s, %d, %s, %s;`,
			fest, gameExpr, seqOf(fest), now, actorSel, reqSel, opCode2int(opCode), payload(table, rowExpr), now)
	}

	name := fmt.Sprintf("journal_%s_%s", table, op)
	switch op {
	case "insert":
		g := gameVia("new")
		return fmt.Sprintf("create trigger %s after insert on %s\nbegin\n  %s\nend",
			name, table, insertSelect(g, "INSERT", rowJSON("new"), "new"))
	case "update":
		g := gameVia("new")
		return fmt.Sprintf("create trigger %s after update on %s\nbegin\n  %s\nend",
			name, table, insertSelect(g, "UPDATE", changedRowJSON("new"), "new"))
	case "delete":
		g := gameVia("old")
		return fmt.Sprintf("create trigger %s after delete on %s\nbegin\n  %s\nend",
			name, table, insertSelect(g, "DELETE", pkJSON("old"), "old"))
	}
	return ""
}

func opCode2int(op string) int {
	switch op {
	case "INSERT":
		return int(opRowIns)
	case "UPDATE":
		return int(opRowSet)
	case "DELETE":
		return int(opRowDel)
	}
	return 0
}

// ensureJournalTriggers (re)installs the journal row-op triggers when the schema
// shape or template version changes. Mirrors ensureAuditTriggers' fingerprinting.
func ensureJournalTriggers(db *sql.DB) error {
	if _, err := db.Exec(`create table if not exists journal_trigger_state(
  id integer primary key check(id = 1),
  fingerprint text not null default ''
);`); err != nil {
		return err
	}

	var fp strings.Builder
	fmt.Fprintf(&fp, "v%d\n", journalTriggerTemplateVersion)
	shapes := map[string][2][]string{}
	for _, t := range journalGameTables {
		cols, pks, err := auditTableShape(db, t.table)
		if err != nil {
			return err
		}
		shapes[t.table] = [2][]string{cols, pks}
		fmt.Fprintf(&fp, "%s|%s|%s\n", t.table, strings.Join(cols, ","), strings.Join(pks, ","))
	}
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(fp.String())))

	var current string
	_ = db.QueryRow(`select fingerprint from journal_trigger_state where id = 1`).Scan(&current)
	if current == sum {
		return nil
	}

	// Drop any existing journal_* triggers, then rebuild.
	rows, err := db.Query(`select name from sqlite_master where type='trigger' and name like 'journal\_%' escape '\'`)
	if err != nil {
		return err
	}
	var drop []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		drop = append(drop, n)
	}
	rows.Close()
	for _, n := range drop {
		if _, err := db.Exec(`drop trigger if exists ` + quoteIdent(n)); err != nil {
			return err
		}
	}
	for _, t := range journalGameTables {
		shape := shapes[t.table]
		for _, op := range []string{"insert", "update", "delete"} {
			if _, err := db.Exec(buildJournalRowTrigger(t.table, shape[0], shape[1], op, t.gameVia)); err != nil {
				return fmt.Errorf("install journal trigger %s/%s: %w", t.table, op, err)
			}
		}
	}
	if _, err := db.Exec(`insert or replace into journal_trigger_state(id, fingerprint) values(1, ?)`, sum); err != nil {
		return err
	}
	return nil
}
