package dopeserver

import (
	"context"
	"database/sql"
	"errors"

	"pecheny.me/dopecore/sqlitex"

	_ "modernc.org/sqlite"

	"dope/dope/platform/util"
	"dope/dope/storage/journal"
	"dope/dope/storage/store"
	"dope/dope/web/route"
)

const (
	dbFile             = "fest.db"
	defaultMatchCode   = "A"
	defaultVenueTitle  = "Москва-1"
	defaultGameCode    = "default"
	systemUserUsername = "system"
)

// openFestDB opens the DB pinned to one connection for the schema work —
// migrations toggle PRAGMA foreign_keys and run multi-statement rewrites, which
// must land on a single connection — then widens the pool for runtime reads.
func openFestDB(path string) (*sql.DB, error) {
	return sqlitex.Open(path, func(db *sql.DB) error {
		// Disarm the journal row-op triggers for the whole schema-migration +
		// data-conversion window: structural churn is not an edit and must never
		// journal. EnsureTriggers reinstalls them before any live write can occur
		// (bootstrap is single-threaded).
		if err := journal.DropTriggers(db); err != nil {
			return err
		}
		if err := migrateDB(db); err != nil {
			return err
		}
		if err := journal.EnsureTriggers(db); err != nil {
			return err
		}
		return journal.BackfillGameCheckpoints(db)
	})
}

// loadActiveContext picks an arbitrary fest/game/first-match to drive the
// transitional single-context handlers. Returns zero values (no error) when the
// DB has no fest yet — that's the default empty state.
func loadActiveContext(db *sql.DB) (festID, gameID int64, matchCode string, err error) {
	row := db.QueryRow(`
select t.id, g.id, coalesce((select m.code from matches m where m.game_id = g.id order by m.position, m.id limit 1), '')
from fests t
join games g on g.fest_id = t.id
order by t.id, g.position, g.id
limit 1`)
	if err = row.Scan(&festID, &gameID, &matchCode); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, "", nil
		}
		return 0, 0, "", err
	}
	return festID, gameID, matchCode, nil
}

func ensureSystemUser(ctx context.Context, tx *sql.Tx) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `select id from users where is_system = 1 limit 1`).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	now := util.UtcNow()
	return store.InsertReturningID(ctx, tx, `
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(null, null, ?, 1, ?, ?)`, systemUserUsername, now, now)
}

func defaultGameID(ctx context.Context, q store.Queryer, festID int64) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `select id from games where fest_id = ? order by position, id limit 1`, festID).Scan(&id)
	return id, err
}

// util.ValidateSlug enforces the slug grammar: 1-64 chars of a-z, 0-9, hyphen;
// the slug cannot be all digits (so it never collides with a numeric ID lookup).

func resolveGameID(ctx context.Context, q store.Queryer, festID int64, ref string) (int64, error) {
	return route.ResolveGameID(ctx, q, festID, ref)
}
