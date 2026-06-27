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

// ArchiveQuiesce is how long a fest must be free of edits before its hot tail is
// eligible for folding. A fest being actively edited (a live event) is left
// entirely in the hot tail until it goes quiet, so the archiver never folds —
// and so never contends on the write path for — a fest mid-match.
const ArchiveQuiesce = 2 * time.Hour

// ArchiveFest folds all hot journal rows for festID with seq <= throughSeq into
// one cold segment, then deletes them. Returns the number of rows archived.
// Idempotent-ish: re-running with the same throughSeq archives nothing.
func ArchiveFest(ctx context.Context, db *sql.DB, festID, throughSeq int64) (int, error) {
	dict, err := LoadWritableDict(db)
	if err != nil {
		return 0, fmt.Errorf("load dict: %w", err)
	}

	rows, err := db.QueryContext(ctx, `
select seq, ts, coalesce(actor_user_id, 0), coalesce(request_id, ''), op, payload
from journal where fest_id = ? and seq <= ? order by seq`, festID, throughSeq)
	if err != nil {
		return 0, err
	}
	var (
		records  []Record
		seqStart int64
		seqEnd   int64
	)
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
			rows.Close()
			return 0, err
		}
		if len(records) == 0 {
			seqStart = seq
		}
		seqEnd = seq
		records = append(records, Record{
			Seq:         uint64(seq),
			Op:          Op(op),
			TSUnixMilli: ParseTSMilli(ts),
			ActorID:     actor,
			RequestID:   dict.Intern(reqID),
			Args:        append([]byte(nil), payload...),
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(records) == 0 {
		return 0, nil
	}

	// Compress off-lock.
	blob := Compress(EncodeSegment(records))

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := dict.PersistTx(tx); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
insert into journal_segment(fest_id, seq_start, seq_end, dsl_version, n_records, blob, created_at)
values(?, ?, ?, ?, ?, ?, ?)`,
		festID, seqStart, seqEnd, DSLVersion, len(records), blob, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `delete from journal where fest_id = ? and seq <= ?`, festID, throughSeq); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(records), nil
}

// ArchiveStale archives every fest's settled hot rows, leaving the newest
// ArchiveKeepRecent rows per fest uncompressed. Safe to call periodically.
func ArchiveStale(ctx context.Context, db *sql.DB) (int, error) {
	// Only consider fests that are over the keep-recent threshold AND have been
	// quiet for ArchiveQuiesce. Rows with a NULL fest_id (the trigger could not
	// resolve the owning fest at write time) are skipped: a single NULL group
	// used to abort the whole pass (scanning NULL into int64 fails), so nothing
	// ever archived and the hot tail grew without bound. migrateDB backfills the
	// resolvable ones from their game; any residual orphans stay out of the way.
	cutoff := time.Now().UTC().Add(-ArchiveQuiesce).Format("2006-01-02T15:04:05Z")
	rows, err := db.QueryContext(ctx, `
select fest_id, max(seq) from journal
where fest_id is not null
group by fest_id
having count(*) > ? and max(unixepoch(ts)) < unixepoch(?)`, ArchiveKeepRecent, cutoff)
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
