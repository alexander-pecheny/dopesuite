package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
)

func openMemDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open mem db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

// TestReplayRowOps applies a hand-built forward history (insert/update/delete)
// to a table and verifies the resulting rows.
func TestReplayRowOps(t *testing.T) {
	db := openMemDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`create table t(id integer primary key, name text, score integer)`); err != nil {
		t.Fatal(err)
	}
	dict := map[uint64]string{1: "t", 2: "id", 3: "name", 4: "score"}
	rp := newJournalReplayer(dict)

	ins := func(seq uint64, id int64, name string, score int64) journalRecord {
		return journalRecord{Seq: seq, Op: opRowIns, Args: encodeRowArgs(rowArgs{tableID: 1, cols: []colVal{
			{2, id}, {3, name}, {4, score}}})}
	}
	recs := []journalRecord{
		ins(1, 1, "a", 10),
		ins(2, 2, "b", 20),
		{Seq: 3, Op: opRowSet, Args: encodeRowArgs(rowArgs{tableID: 1, cols: []colVal{{2, int64(1)}, {4, int64(15)}}})},
		{Seq: 4, Op: opRowDel, Args: encodeRowArgs(rowArgs{tableID: 1, cols: []colVal{{2, int64(2)}}})},
	}

	tx, _ := db.Begin()
	if err := rp.applyAll(ctx, tx, recs); err != nil {
		t.Fatalf("applyAll: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var name string
	var score int64
	if err := db.QueryRow(`select name, score from t where id=1`).Scan(&name, &score); err != nil {
		t.Fatalf("row 1: %v", err)
	}
	if name != "a" || score != 15 {
		t.Fatalf("row 1 = (%q,%d), want (a,15)", name, score)
	}
	var n int
	db.QueryRow(`select count(*) from t where id=2`).Scan(&n)
	if n != 0 {
		t.Fatalf("row 2 should be deleted, got %d", n)
	}
}

// TestConvertThenReplay drives the full P1+P2 path on controlled data: build a
// synthetic before/after audit_log representing a forward history from empty,
// convert it to journal segments, then replay those segments into a fresh table
// and assert the reconstructed state matches the expected final state.
func TestConvertThenReplay(t *testing.T) {
	db := openMemDB(t)
	ctx := context.Background()
	mustExec(t, db, `create table widgets(id integer primary key, fest_id integer, label text, val integer)`)
	mustExec(t, db, `create table audit_log(
  id integer primary key, ts text, table_name text, row_pk text, op text,
  before_json text, after_json text, actor_user_id integer, request_id text, fest_id integer)`)

	type al struct {
		op     string
		before string
		after  string
		pk     string
	}
	entries := []al{
		{"INSERT", "", `{"id":1,"fest_id":1,"label":"a","val":10}`, "1"},
		{"INSERT", "", `{"id":2,"fest_id":1,"label":"b","val":20}`, "2"},
		{"UPDATE", `{"id":1,"val":10}`, `{"id":1,"val":15}`, "1"},
		{"DELETE", `{"id":2,"fest_id":1,"label":"b","val":20}`, "", "2"},
	}
	for i, e := range entries {
		var bj, aj any
		if e.before != "" {
			bj = e.before
		}
		if e.after != "" {
			aj = e.after
		}
		mustExec(t, db, `insert into audit_log(id, ts, table_name, row_pk, op, before_json, after_json, actor_user_id, request_id, fest_id)
values(?, '2026-01-01T00:00:00.000Z', 'widgets', ?, ?, ?, ?, 7, 'req1', 1)`,
			i+1, e.pk, e.op, bj, aj)
	}

	rep, err := convertAuditLog(db)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if rep.journalRecords != 4 || rep.segments != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}

	// Replay segments into a fresh widgets table (genesis = empty).
	mustExec(t, db, `delete from widgets`)
	dict, err := loadJournalDict(ctx, db)
	if err != nil {
		t.Fatalf("load dict: %v", err)
	}
	rp := newJournalReplayer(dict)
	recs := decodeAllSegments(t, db)
	tx, _ := db.Begin()
	if err := rp.applyAll(ctx, tx, recs); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var label string
	var val int64
	if err := db.QueryRow(`select label, val from widgets where id=1`).Scan(&label, &val); err != nil {
		t.Fatalf("widget 1: %v", err)
	}
	if label != "a" || val != 15 {
		t.Fatalf("widget 1 = (%q,%d), want (a,15)", label, val)
	}
	var cnt int
	db.QueryRow(`select count(*) from widgets`).Scan(&cnt)
	if cnt != 1 {
		t.Fatalf("expected 1 widget after replay, got %d", cnt)
	}
}

func decodeAllSegments(t *testing.T, db *sql.DB) []journalRecord {
	t.Helper()
	rows, err := db.Query(`select blob from journal_segment order by fest_id, seq_start`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var all []journalRecord
	for rows.Next() {
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			t.Fatal(err)
		}
		raw, err := zstdDecompress(blob)
		if err != nil {
			t.Fatal(err)
		}
		recs, err := decodeSegment(raw)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, recs...)
	}
	return all
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// TestConvertReplayEquivalenceRealDB is the integrity canary against a real DB
// copy. It is skipped unless DOPE_JOURNAL_TEST_DB points at a COPY of a fest DB
// that still has audit_log. It converts the log and verifies every journal
// record losslessly reproduces the forward content of its audit_log row.
func TestConvertReplayEquivalenceRealDB(t *testing.T) {
	path := os.Getenv("DOPE_JOURNAL_TEST_DB")
	if path == "" {
		t.Skip("set DOPE_JOURNAL_TEST_DB to a fest DB copy to run the real-data canary")
	}
	db, err := sql.Open("sqlite", buildSqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	if _, err := convertAuditLog(db); err != nil {
		t.Fatalf("convert: %v", err)
	}
	dict, err := loadJournalDict(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	rp := newJournalReplayer(dict)
	recs := decodeAllSegments(t, db)
	bySeq := map[uint64]journalRecord{}
	for _, r := range recs {
		bySeq[r.Seq] = r
	}

	rows, err := db.Query(`select id, table_name, op, dope_unz(before_json), dope_unz(after_json) from audit_log order by id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var checked int
	for rows.Next() {
		var id int64
		var table, op string
		var bj, aj sql.NullString
		if err := rows.Scan(&id, &table, &op, &bj, &aj); err != nil {
			t.Fatal(err)
		}
		rec, ok := bySeq[uint64(id)]
		if !ok {
			continue // skipped (null snapshot) — accounted for in the report
		}
		gotTable, gotRow, err := rp.rowFromArgs(rec)
		if err != nil {
			t.Fatalf("seq %d decode: %v", id, err)
		}
		if gotTable != table {
			t.Fatalf("seq %d table = %q want %q", id, gotTable, table)
		}
		var want map[string]any
		switch op {
		case "INSERT", "UPDATE":
			want, _ = decodeRowJSON(aj.String)
		case "DELETE":
			// only pk columns are kept; got must be a subset of the full row
			want, _ = decodeRowJSON(bj.String)
		}
		if err := assertRowSubset(gotRow, want, op); err != nil {
			t.Fatalf("seq %d (%s %s): %v", id, op, table, err)
		}
		checked++
	}
	t.Logf("real-DB canary: verified %d journal records against audit_log", checked)
}

func assertRowSubset(got, want map[string]any, op string) error {
	// For INSERT/UPDATE got must equal want; for DELETE got (pk-only) must be a
	// subset of want with matching values.
	if op == "DELETE" {
		for k, v := range got {
			if !sameSQLValue(want[k], v) {
				return fmt.Errorf("pk col %q got %v want %v", k, v, want[k])
			}
		}
		return nil
	}
	if len(got) != len(want) {
		return fmt.Errorf("col count got %d want %d", len(got), len(want))
	}
	for k, v := range want {
		if !sameSQLValue(v, got[k]) {
			return fmt.Errorf("col %q got %v want %v", k, got[k], v)
		}
	}
	return nil
}

func sameSQLValue(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}
