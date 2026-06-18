package migrate

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"dope/dope/journal"
	"dope/dope/store"
	"dope/dope/storeutil"
)

// RunConvertAudit is a one-time / measurement subcommand: it reads the legacy
// before/after-snapshot audit_log and re-encodes it as the new forward journal
// (generic row-delta opcodes), compresses it into per-fest cold segments, and
// prints the on-disk size before/after. It is the conversion + savings
// measurement called for in the redesign.
//
// Run against a COPY of the database (it writes journal_* tables into it):
//
//	dope-server convert-audit --db /tmp/fest-copy.db
//
// Use --drop-audit to also drop the audit_log afterwards and VACUUM, to measure
// the realized file-size reduction.
func RunConvertAudit(args []string) {
	fs := flag.NewFlagSet("convert-audit", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to a COPY of the sqlite database")
	dropAudit := fs.Bool("drop-audit", false, "drop audit_log and VACUUM after conversion (measures realized file shrink)")
	_ = fs.Parse(args)
	if *dbPath == "" {
		log.Fatal("convert-audit: --db is required (use a COPY, not the live DB)")
	}

	db, err := sql.Open("sqlite", store.BuildDSN(*dbPath))
	if err != nil {
		log.Fatalf("convert-audit: open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	start := time.Now()
	rep, err := ConvertAuditLog(db)
	if err != nil {
		log.Fatalf("convert-audit: %v", err)
	}
	rep.print()
	log.Printf("convert-audit: converted in %s", time.Since(start).Round(time.Millisecond))

	if *dropAudit {
		log.Printf("convert-audit: dropping audit_log and vacuuming…")
		if _, err := db.Exec(`drop table if exists audit_log`); err != nil {
			log.Fatalf("convert-audit: drop audit_log: %v", err)
		}
		if _, err := db.Exec(`VACUUM`); err != nil {
			log.Fatalf("convert-audit: vacuum: %v", err)
		}
		var pages, pageSize int64
		_ = db.QueryRow(`pragma page_count`).Scan(&pages)
		_ = db.QueryRow(`pragma page_size`).Scan(&pageSize)
		log.Printf("convert-audit: final db file size: %s", humanBytes(pages*pageSize))
	}
}

// ConvertReport holds the measurement results.
type ConvertReport struct {
	auditRows      int64
	auditBytes     int64 // audit_log table + indexes, on-disk pages
	skipped        int64
	JournalRecords int64
	Segments       int64
	rawStreamBytes int64 // pre-compression DSL stream
	segmentBytes   int64 // journal_segment + journal_dict, on-disk pages
}

func (r ConvertReport) print() {
	log.Printf("convert-audit: ── measurement ──")
	log.Printf("  old audit_log:     %10s  (%d rows%s)", humanBytes(r.auditBytes), r.auditRows, skippedNote(r.skipped))
	log.Printf("  new journal (raw): %10s  (%d records, pre-zstd stream)", humanBytes(r.rawStreamBytes), r.JournalRecords)
	log.Printf("  new journal (disk):%10s  (%d cold segments + dict)", humanBytes(r.segmentBytes), r.Segments)
	if r.segmentBytes > 0 {
		log.Printf("  savings:           %.1fx smaller  (%.1f%% reduction)",
			float64(r.auditBytes)/float64(r.segmentBytes),
			100*(1-float64(r.segmentBytes)/float64(r.auditBytes)))
	}
}

func skippedNote(n int64) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", %d skipped", n)
}

func humanBytes(n int64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(u), 0
	for x := n / u; x >= u; x /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(n)/float64(div), "KMGT"[exp])
}

func loadWritableJournalDict(db interface {
	Query(string, ...any) (*sql.Rows, error)
}) (*journal.Dict, error) {
	return journal.LoadWritableDict(db)
}

func ConvertAuditLog(db *sql.DB) (ConvertReport, error) {
	var rep ConvertReport
	if err := journal.CreateTables(db); err != nil {
		return rep, fmt.Errorf("create journal tables: %w", err)
	}

	rep.auditBytes = objectBytes(db, "audit_log")
	_ = db.QueryRow(`select count(*) from audit_log`).Scan(&rep.auditRows)

	dict := journal.NewDict()

	// Pre-load every table's shape before opening the audit cursor: the pool is
	// pinned to one connection, so issuing pragma_table_info queries while the
	// audit_log cursor is open would deadlock.
	shapes := map[string]auditShape{}
	tnRows, err := db.Query(`select distinct table_name from audit_log`)
	if err != nil {
		return rep, fmt.Errorf("list tables: %w", err)
	}
	var tableNames []string
	for tnRows.Next() {
		var t string
		if err := tnRows.Scan(&t); err != nil {
			tnRows.Close()
			return rep, err
		}
		tableNames = append(tableNames, t)
	}
	tnRows.Close()
	for _, t := range tableNames {
		cols, pks, err := store.TableShape(db, t)
		if err != nil {
			return rep, fmt.Errorf("shape %s: %w", t, err)
		}
		shapes[t] = auditShape{cols: cols, pks: pks}
	}

	rows, err := db.Query(`
select id, ts, table_name, op,
       dope_unz(before_json) as bj,
       dope_unz(after_json)  as aj,
       coalesce(actor_user_id, 0),
       coalesce(request_id, ''),
       coalesce(fest_id, 0)
from audit_log
order by id`)
	if err != nil {
		return rep, fmt.Errorf("scan audit_log: %w", err)
	}
	defer rows.Close()

	// Group records per fest bucket; each bucket becomes one cold segment.
	buckets := map[int64][]journal.Record{}

	for rows.Next() {
		var (
			id        int64
			ts        string
			table     string
			op        string
			bj, aj    sql.NullString
			actor     int64
			requestID string
			festID    int64
		)
		if err := rows.Scan(&id, &ts, &table, &op, &bj, &aj, &actor, &requestID, &festID); err != nil {
			return rep, err
		}

		shape := shapes[table]

		rec, ok, err := buildRowRecord(dict, table, shape, op, id, ts, actor, requestID, bj, aj)
		if err != nil {
			return rep, fmt.Errorf("audit id %d (%s %s): %w", id, op, table, err)
		}
		if !ok {
			rep.skipped++
			continue
		}
		buckets[festID] = append(buckets[festID], rec)
		rep.JournalRecords++
	}
	if err := rows.Err(); err != nil {
		return rep, err
	}

	if err := dict.Persist(db); err != nil {
		return rep, fmt.Errorf("persist dict: %w", err)
	}

	// Stable fest order for deterministic output.
	festIDs := make([]int64, 0, len(buckets))
	for fid := range buckets {
		festIDs = append(festIDs, fid)
	}
	sort.Slice(festIDs, func(i, j int) bool { return festIDs[i] < festIDs[j] })

	now := time.Now().UTC().Format(time.RFC3339)
	for _, fid := range festIDs {
		recs := buckets[fid]
		sort.Slice(recs, func(i, j int) bool { return recs[i].Seq < recs[j].Seq })
		raw := journal.EncodeSegment(recs)
		rep.rawStreamBytes += int64(len(raw))
		blob := journal.Compress(raw)
		if _, err := db.Exec(`
insert into journal_segment(fest_id, seq_start, seq_end, dsl_version, n_records, blob, created_at)
values(?, ?, ?, ?, ?, ?, ?)`,
			fid, recs[0].Seq, recs[len(recs)-1].Seq, journal.DSLVersion, len(recs), blob, now); err != nil {
			return rep, fmt.Errorf("insert segment fest %d: %w", fid, err)
		}
		rep.Segments++
	}

	rep.segmentBytes = objectBytes(db, "journal_segment") + objectBytes(db, "journal_dict")
	return rep, nil
}

type auditShape struct {
	cols []string
	pks  []string
}

// buildRowRecord turns one audit_log row into a generic row-op journal record.
// Returns ok=false when the row carries no usable snapshot (skipped).
func buildRowRecord(dict *journal.Dict, table string, shape auditShape, op string, id int64, ts string, actor int64, requestID string, bj, aj sql.NullString) (journal.Record, bool, error) {
	tableID := dict.Intern(table)
	var (
		opCode journal.Op
		src    sql.NullString
		pkOnly bool
	)
	switch op {
	case "INSERT":
		opCode, src = journal.OpRowIns, aj // full row
	case "UPDATE":
		opCode, src = journal.OpRowSet, aj // pk + changed cols
	case "DELETE":
		opCode, src, pkOnly = journal.OpRowDel, bj, true // full row, keep pk only
	default:
		return journal.Record{}, false, fmt.Errorf("unknown op %q", op)
	}
	if !src.Valid || src.String == "" {
		return journal.Record{}, false, nil
	}
	rowMap, err := DecodeRowJSON(src.String)
	if err != nil {
		return journal.Record{}, false, err
	}
	if len(rowMap) == 0 {
		return journal.Record{}, false, nil
	}

	var cols []journal.ColVal
	if pkOnly && len(shape.pks) > 0 {
		// DELETE: keep only the primary-key columns — that's all forward replay
		// needs, and dropping the full before-row is a large part of the savings.
		for _, pk := range shape.pks {
			if v, ok := rowMap[pk]; ok {
				cols = append(cols, journal.ColVal{NameID: dict.Intern(pk), Val: v})
			}
		}
		if len(cols) == 0 {
			// No declared-PK values present; fall back to the whole row.
			cols = colsFromMap(dict, rowMap)
		}
	} else {
		cols = colsFromMap(dict, rowMap)
	}

	rec := journal.Record{
		Seq:         uint64(id),
		Op:          opCode,
		TSUnixMilli: journal.ParseTSMilli(ts),
		ActorID:     actor,
		RequestID:   dict.Intern(requestID),
		Args:        journal.EncodeRowArgs(journal.RowArgs{TableID: tableID, Cols: cols}),
	}
	return rec, true, nil
}

func colsFromMap(dict *journal.Dict, m map[string]any) []journal.ColVal {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic column order
	cols := make([]journal.ColVal, 0, len(keys))
	for _, k := range keys {
		cols = append(cols, journal.ColVal{NameID: dict.Intern(k), Val: m[k]})
	}
	return cols
}

// DecodeRowJSON parses a row snapshot, keeping integers as int64 (reusing the
// same number coercion as the revert path).
func DecodeRowJSON(s string) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		out[k] = storeutil.JSONToSQLValue(v)
	}
	return out, nil
}

// objectBytes returns the on-disk page bytes for a table or index (and any
// auto-index) via dbstat.
func objectBytes(db *sql.DB, table string) int64 {
	var total int64
	_ = db.QueryRow(`
select coalesce(sum(pgsize), 0) from dbstat
where name = ?
   or name in (select name from sqlite_master where type='index' and tbl_name = ?)`,
		table, table).Scan(&total)
	return total
}
