package journal

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"
)

// newArchiveTestDB builds a minimal DB with just the tables ArchiveFest touches.
func newArchiveTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// :memory: is per-connection, so pin the pool to one shared connection.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	stmts := []string{
		`create table journal(
  id integer primary key, fest_id integer, game_id integer, seq integer not null,
  ts text not null, actor_user_id integer, request_id text, op integer not null,
  payload blob not null default x'', created_at text not null)`,
		`create table journal_segment(
  id integer primary key, fest_id integer not null, seq_start integer not null,
  seq_end integer not null, dsl_version integer not null, n_records integer not null,
  blob blob not null, created_at text not null)`,
		`create table journal_dict(id integer primary key, str text not null unique)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return db
}

// TestArchiveFestChunkedLossless folds a backlog far larger than the chunk
// budget and verifies the cold segments reconstruct every archived row exactly,
// the hot rows up to the cutoff are gone, and rows past the cutoff are kept.
func TestArchiveFestChunkedLossless(t *testing.T) {
	db := newArchiveTestDB(t)

	// Force many small chunks so the seq-boundary cutting logic is exercised.
	orig := archiveChunkBytes
	archiveChunkBytes = 256
	t.Cleanup(func() { archiveChunkBytes = orig })

	const festID = 1
	const total = 500
	const through = 400 // archive seq 1..400; keep 401..500

	type want struct {
		op      int
		payload []byte
	}
	wants := map[int64]want{}
	for i := 1; i <= total; i++ {
		// Vary payload size so some single rows alone exceed the budget.
		payload := []byte(fmt.Sprintf("row-%d-%s", i, repeat('x', i%200)))
		op := i % 4
		if _, err := db.Exec(
			`insert into journal(fest_id, seq, ts, op, request_id, payload, created_at)
values(?, ?, ?, ?, ?, ?, ?)`,
			festID, i, "2026-01-01T00:00:00Z", op, fmt.Sprintf("req-%d", i%7), payload, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
		if int64(i) <= through {
			wants[int64(i)] = want{op: op, payload: payload}
		}
	}

	n, err := ArchiveFest(context.Background(), db, festID, through)
	if err != nil {
		t.Fatalf("ArchiveFest: %v", err)
	}
	if n != through {
		t.Fatalf("archived %d rows, want %d", n, through)
	}

	// Hot rows: only the kept tail (401..500) should remain.
	var hot, minSeq int
	if err := db.QueryRow(`select count(*), coalesce(min(seq),0) from journal`).Scan(&hot, &minSeq); err != nil {
		t.Fatal(err)
	}
	if hot != total-through {
		t.Fatalf("hot rows left = %d, want %d", hot, total-through)
	}
	if minSeq != through+1 {
		t.Fatalf("lowest surviving seq = %d, want %d", minSeq, through+1)
	}

	// Multiple segments must have been written (proves chunking actually split).
	var segs int
	if err := db.QueryRow(`select count(*) from journal_segment`).Scan(&segs); err != nil {
		t.Fatal(err)
	}
	if segs < 2 {
		t.Fatalf("expected multiple cold segments, got %d", segs)
	}

	// Reconstruct every archived row from the segments and compare to the source.
	got := map[int64]want{}
	rows, err := db.Query(`select blob from journal_segment order by seq_start`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var prevEnd int64
	for rows.Next() {
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			t.Fatal(err)
		}
		raw, err := Decompress(blob)
		if err != nil {
			t.Fatalf("decompress: %v", err)
		}
		recs, err := DecodeSegment(raw)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		for _, r := range recs {
			if int64(r.Seq) <= prevEnd {
				t.Fatalf("seq %d out of order across segments (prev end %d)", r.Seq, prevEnd)
			}
			got[int64(r.Seq)] = want{op: int(r.Op), payload: r.Args}
		}
		if len(recs) > 0 {
			prevEnd = int64(recs[len(recs)-1].Seq)
		}
	}

	if len(got) != len(wants) {
		t.Fatalf("reconstructed %d rows, want %d", len(got), len(wants))
	}
	for seq, w := range wants {
		g, ok := got[seq]
		if !ok {
			t.Fatalf("seq %d missing from segments", seq)
		}
		if g.op != w.op || string(g.payload) != string(w.payload) {
			t.Fatalf("seq %d mismatch: got op=%d payload=%q, want op=%d payload=%q",
				seq, g.op, g.payload, w.op, w.payload)
		}
	}
}

// TestArchiveFestSkipsNonMatching confirms a fest with no rows <= through is a
// no-op (idempotent re-runs archive nothing).
func TestArchiveFestSkipsNonMatching(t *testing.T) {
	db := newArchiveTestDB(t)
	if _, err := db.Exec(
		`insert into journal(fest_id, seq, ts, op, payload, created_at)
values(1, 10, '2026-01-01T00:00:00Z', 1, x'00', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	n, err := ArchiveFest(context.Background(), db, 1, 5) // nothing at seq <= 5
	if err != nil {
		t.Fatalf("ArchiveFest: %v", err)
	}
	if n != 0 {
		t.Fatalf("archived %d, want 0", n)
	}
}

func repeat(c byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}
