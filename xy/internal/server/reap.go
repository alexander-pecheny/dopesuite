package server

import (
	"context"
	"database/sql"
	"log"
	"time"
)

// tombstoneTTL is how long a deleted entity stays restorable before the reaper
// destroys it — matched to the litestream snapshot retention so the recovery
// window is uniform across rows and backups (ADR-0002).
const tombstoneTTL = 14 * 24 * time.Hour

const reapInterval = time.Hour

// orphanBlobMinAge guards the orphan sweep against racing an upload whose row
// hasn't committed yet: only unreferenced blobs at least this old are removed.
const orphanBlobMinAge = time.Hour

// reapOnce hard-deletes every tombstone with deleted_at before cutoff. Blob refs
// are collected first (an attachment dies with its own tombstone or any expired
// ancestor's), rows go in one transaction via FK cascade, and blob files are
// removed after commit, so a crash can only leak blobs (the orphan sweep's job),
// never dangle a row.
func (s *server) reapOnce(ctx context.Context, cutoff time.Time) (rowsReaped int64, err error) {
	cut := rfc3339(cutoff)
	var refs []string
	err = s.withWriteTx(ctx, "reap", func(ctx context.Context, tx *sql.Tx) error {
		refs, rowsReaped = nil, 0
		rows, err := tx.QueryContext(ctx, `
select a.blob_ref from attachments a
join boards b on b.id = a.board_id
join cards c on c.id = a.card_id
join lists l on l.id = c.list_id
where b.deleted_at < ?1 or c.deleted_at < ?1 or l.deleted_at < ?1 or a.deleted_at < ?1`, cut)
		if err != nil {
			return err
		}
		for rows.Next() {
			var ref string
			if err := rows.Scan(&ref); err != nil {
				rows.Close()
				return err
			}
			refs = append(refs, ref)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		// reply_to_id and lists.group_id have no ON DELETE action, so live rows
		// pointing at a reaped one must be unlinked first.
		stmts := []string{
			`update timeline_events set reply_to_id = null
			 where reply_to_id in (select id from timeline_events where deleted_at < ?1)`,
			`update lists set group_id = null
			 where group_id in (select id from list_groups where deleted_at < ?1)`,
			`delete from boards where deleted_at < ?1`,
			`delete from cards where deleted_at < ?1`,
			`delete from lists where deleted_at < ?1`,
			`delete from list_groups where deleted_at < ?1`,
			`delete from labels where deleted_at < ?1`,
			`delete from timeline_events where deleted_at < ?1`,
			`delete from attachments where deleted_at < ?1`,
		}
		for _, q := range stmts {
			res, err := tx.ExecContext(ctx, q, cut)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err == nil {
				rowsReaped += n
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, ref := range refs {
		_ = s.blobs.Remove(ref)
	}
	return rowsReaped, nil
}

// sweepOrphanBlobs removes blob files no attachment row references — leaked by
// a crash between a transaction and its blob Remove. minAge keeps it clear of
// uploads whose row hasn't committed yet.
func (s *server) sweepOrphanBlobs(ctx context.Context, minAge time.Duration) (int, error) {
	onDisk, err := s.blobs.ListOlderThan(time.Now().Add(-minAge))
	if err != nil {
		return 0, err
	}
	rows, err := s.db.QueryContext(ctx, `select blob_ref from attachments`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	referenced := make(map[string]bool)
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return 0, err
		}
		referenced[ref] = true
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, ref := range onDisk {
		if !referenced[ref] {
			if s.blobs.Remove(ref) == nil {
				n++
			}
		}
	}
	return n, nil
}

func (s *server) reap() {
	ctx := context.Background()
	if n, err := s.reapOnce(ctx, time.Now().Add(-tombstoneTTL)); err != nil {
		log.Printf("reap: %v", err)
	} else if n > 0 {
		log.Printf("reap: destroyed %d expired tombstone rows", n)
	}
	if n, err := s.sweepOrphanBlobs(ctx, orphanBlobMinAge); err != nil {
		log.Printf("orphan sweep: %v", err)
	} else if n > 0 {
		log.Printf("orphan sweep: removed %d blobs", n)
	}
}

func (s *server) reapLoop() {
	t := time.NewTicker(reapInterval)
	defer t.Stop()
	for range t.C {
		s.reap()
	}
}

func runGC() {
	srv, err := newServer()
	if err != nil {
		log.Fatal(err)
	}
	srv.reap()
}
