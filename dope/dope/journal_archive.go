package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
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
// so we read the run, compress it, then take a short write transaction only to
// insert the segment and delete the hot rows.

// journalArchiveKeepRecent is how many of the newest hot rows per fest are left
// uncompressed so live resync can still serve them as individual events.
const journalArchiveKeepRecent = 200

// archiveFestJournal folds all hot journal rows for festID with seq <=
// throughSeq into one cold segment, then deletes them. Returns the number of
// rows archived. Idempotent-ish: re-running with the same throughSeq archives
// nothing (rows already gone) and writes no empty segment.
func archiveFestJournal(ctx context.Context, db *sql.DB, festID, throughSeq int64) (int, error) {
	dict, err := loadWritableJournalDict(db)
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
		records  []journalRecord
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
		records = append(records, journalRecord{
			Seq:         uint64(seq),
			Op:          journalOp(op),
			TSUnixMilli: parseTSMilli(ts),
			ActorID:     actor,
			RequestID:   dict.intern(reqID),
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
	blob := zstdCompress(encodeSegment(records))

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := dict.persistTx(tx); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
insert into journal_segment(fest_id, seq_start, seq_end, dsl_version, n_records, blob, created_at)
values(?, ?, ?, ?, ?, ?, ?)`,
		festID, seqStart, seqEnd, journalDSLVersion, len(records), blob, utcNow()); err != nil {
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

// archiveStaleJournals archives every fest's settled hot rows, leaving the
// newest journalArchiveKeepRecent rows per fest uncompressed. Safe to call
// periodically; it no-ops when there's nothing to fold.
func archiveStaleJournals(ctx context.Context, db *sql.DB) (int, error) {
	rows, err := db.QueryContext(ctx, `
select fest_id, max(seq) from journal group by fest_id having count(*) > ?`, journalArchiveKeepRecent)
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
		// Archive everything except the most recent keepRecent rows. The cutoff
		// is approximate (seq has no gaps in normal operation), which is fine —
		// archiving is just compaction.
		through := t.maxSeq - journalArchiveKeepRecent
		if through <= 0 {
			continue
		}
		n, err := archiveFestJournal(ctx, db, t.festID, through)
		if err != nil {
			return total, fmt.Errorf("archive fest %d: %w", t.festID, err)
		}
		total += n
	}
	return total, nil
}

// journalArchiveInterval controls how often the background archiver runs.
const journalArchiveInterval = 6 * time.Hour

// initJournalArchive launches the periodic hot->cold folding. Call once from
// main() after the schema is installed. The archiver holds the global write
// lock only for the short insert/delete transaction (compression runs off-lock).
func (s *server) initJournalArchive() {
	if s.db == nil {
		return
	}
	go s.runJournalArchive(journalArchiveInterval)
}

func (s *server) runJournalArchive(interval time.Duration) {
	// First pass shortly after boot, then settle onto the steady cadence.
	timer := time.NewTimer(10 * time.Minute)
	defer timer.Stop()
	for range timer.C {
		// Bound the archive's DB work with writeTxTimeout so this background pass
		// can never pin the global write lock indefinitely (e.g. on a starved
		// connection pool) and freeze live edits — the 2026-06-13 failure mode.
		ctx, cancel := context.WithTimeout(context.Background(), writeTxTimeout)
		s.mu.Lock()
		n, err := archiveStaleJournals(ctx, s.db)
		s.mu.Unlock()
		cancel()
		if err != nil {
			log.Printf("journal archive: %v", err)
		} else if n > 0 {
			log.Printf("journal archive: folded %d hot rows into cold segments", n)
		}
		timer.Reset(interval)
	}
}
