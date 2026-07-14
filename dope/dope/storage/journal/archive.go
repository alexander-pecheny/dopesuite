package journal

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// The journal hot tail holds recent edits as individual rows (cheap to append,
// indexable, and carrying the SSE payload for live resync). Once a run of edits
// is settled, the archiver folds it into a single zstd-compressed cold segment
// and deletes the hot rows. Cold segments are append-only and never pruned —
// the log lives forever, but compactly: tiny near-identical edits compress
// dramatically better as one stream than as individual rows.
//
// Compression happens OFF the write lock: past seqs are immutable (append-only),
// so the caller reads the run, compresses it, then takes a short write
// transaction only to insert the segment and delete the hot rows.

// ArchiveKeepRecent is how many of the newest hot rows per fest are left
// uncompressed so live resync can still serve them as individual events.
const ArchiveKeepRecent = 200

// ArchiveInterval controls how often the background archiver runs.
const ArchiveInterval = 6 * time.Hour

// ArchiveQuiesce is how long the WHOLE system must be free of edits before the
// background archiver will fold. Gating on global (not per-fest) quiescence lets
// the fold hold the write lock past the live 5s budget without risking a stall:
// if nothing anywhere has been edited for this long, nothing is mid-match.
const ArchiveQuiesce = 2 * time.Hour

// ArchiveMaxHold caps how long one background fold may hold the write lock after
// confirming global quiescence. Far above the 5s live-edit budget because, the
// system being quiet, a longer hold won't stall a match; still bounded so a
// stray edit arriving mid-fold waits at most this long.
const ArchiveMaxHold = 2 * time.Minute

// GloballyQuiet reports whether no journal row has been written within the last
// `within` — i.e. the entire system has been edit-free for that long. The
// background archiver gates on this so a long fold only ever runs when nothing
// is mid-match.
func GloballyQuiet(ctx context.Context, db *sql.DB, within time.Duration) (bool, error) {
	var lastEdit sql.NullInt64
	if err := db.QueryRowContext(ctx, `select max(unixepoch(ts)) from journal`).Scan(&lastEdit); err != nil {
		return false, err
	}
	if !lastEdit.Valid {
		return true, nil // empty journal
	}
	return lastEdit.Int64 < time.Now().UTC().Add(-within).Unix(), nil
}

// archiveChunkBytes bounds the raw payload pulled into memory per cold segment.
// A fest's whole backlog can be hundreds of MB (op=ROWSET full state_json
// snapshots), and folding it in a single segment OOM-killed the process on the
// 1GB prod box. So fold in bounded chunks: peak memory stays ~chunk-sized
// regardless of backlog size, and a large backlog simply drains as several
// append-only segments (replayed in order, so multiple segments are transparent).
// A var, not a const, only so tests can lower it to force multi-chunk folds.
var archiveChunkBytes = 16 << 20

// ArchiveFest folds hot journal rows for festID with seq <= throughSeq into one
// or more cold segments (each holding up to ~archiveChunkBytes of raw payload),
// deleting the rows as it goes. Returns the number of rows archived.
// Idempotent-ish: re-running with the same throughSeq archives nothing. Chunk
// boundaries always fall between seqs, so the delete never drops a row that
// wasn't archived into a segment first.
func ArchiveFest(ctx context.Context, db *sql.DB, festID, throughSeq int64) (int, error) {
	dict, err := LoadWritableDict(db)
	if err != nil {
		return 0, fmt.Errorf("load dict: %w", err)
	}

	total := 0
	afterSeq := int64(-1) // seq is a non-negative fest revision; -1 is below any row
	for {
		records, seqStart, seqEnd, err := readArchiveChunk(ctx, db, dict, festID, afterSeq, throughSeq)
		if err != nil {
			return total, err
		}
		if len(records) == 0 {
			return total, nil
		}

		blob := Compress(EncodeSegment(records))

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return total, err
		}
		if err := dict.PersistTx(tx); err != nil {
			tx.Rollback()
			return total, err
		}
		if _, err := tx.ExecContext(ctx, `
insert into journal_segment(fest_id, seq_start, seq_end, dsl_version, n_records, blob, created_at)
values(?, ?, ?, ?, ?, ?, ?)`,
			festID, seqStart, seqEnd, DSLVersion, len(records), blob, time.Now().UTC().Format(time.RFC3339)); err != nil {
			tx.Rollback()
			return total, err
		}
		if _, err := tx.ExecContext(ctx,
			`delete from journal where fest_id = ? and seq > ? and seq <= ?`, festID, afterSeq, seqEnd); err != nil {
			tx.Rollback()
			return total, err
		}
		if err := tx.Commit(); err != nil {
			return total, err
		}
		total += len(records)
		afterSeq = seqEnd
	}
}

// readArchiveChunk reads the next run of rows (seq in (afterSeq, throughSeq]) up
// to ~archiveChunkBytes of raw payload, always stopping on a seq boundary so a
// single seq is never split across two segments (the delete keys on seq). The
// row that trips the budget is left unread for the next chunk's query to pick up.
func readArchiveChunk(ctx context.Context, db *sql.DB, dict *Dict, festID, afterSeq, throughSeq int64) (recs []Record, seqStart, seqEnd int64, err error) {
	rows, err := db.QueryContext(ctx, `
select seq, ts, coalesce(actor_user_id, 0), coalesce(request_id, ''), op, payload
from journal where fest_id = ? and seq > ? and seq <= ? order by seq`, festID, afterSeq, throughSeq)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	var nbytes int
	for rows.Next() {
		var (
			seq     int64
			ts      string
			actor   int64
			reqID   string
			op      int
			payload []byte
		)
		if err := rows.Scan(&seq, &ts, &actor, &reqID, &op, &payload); err != nil {
			return nil, 0, 0, err
		}
		if len(recs) > 0 && nbytes >= archiveChunkBytes && seq != seqEnd {
			break // over budget and at a seq boundary
		}
		if len(recs) == 0 {
			seqStart = seq
		}
		seqEnd = seq
		nbytes += len(payload)
		recs = append(recs, Record{
			Seq:         uint64(seq),
			Op:          Op(op),
			TSUnixMilli: ParseTSMilli(ts),
			ActorID:     actor,
			RequestID:   dict.Intern(reqID),
			Args:        append([]byte(nil), payload...),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, err
	}
	return recs, seqStart, seqEnd, nil
}

// ArchiveStale archives every fest's settled hot rows, leaving the newest
// ArchiveKeepRecent rows per fest uncompressed. Safe to call periodically.
func ArchiveStale(ctx context.Context, db *sql.DB) (int, error) {
	// Rows with a NULL fest_id (the trigger could not resolve the owning fest at
	// write time) are skipped: a single NULL group used to abort the whole pass
	// (scanning NULL into int64 fails), so nothing ever archived and the hot tail
	// grew without bound. migrateDB backfills the resolvable ones from their game;
	// any residual orphans stay out of the way.
	//
	// This folds every eligible fest unconditionally — deciding WHEN it is safe to
	// run (so a large fold can't stall a live match) is the caller's job: the
	// background archiver gates on GloballyQuiet; the archive-journal subcommand is
	// run by an operator with the service stopped.
	rows, err := db.QueryContext(ctx, `
select fest_id, max(seq) from journal
where fest_id is not null
group by fest_id having count(*) > ?`, ArchiveKeepRecent)
	if err != nil {
		return 0, err
	}
	type target struct{ festID, maxSeq int64 }
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.festID, &t.maxSeq); err != nil {
			rows.Close()
			return 0, err
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	var total int
	for _, t := range targets {
		// Archive everything except the most recent keepRecent rows. The cutoff is
		// approximate (seq has no gaps in normal operation), which is fine —
		// archiving is just compaction.
		through := t.maxSeq - ArchiveKeepRecent
		if through <= 0 {
			continue
		}
		n, err := ArchiveFest(ctx, db, t.festID, through)
		if err != nil {
			return total, fmt.Errorf("archive fest %d: %w", t.festID, err)
		}
		total += n
	}
	return total, nil
}
