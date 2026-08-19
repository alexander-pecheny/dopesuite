// Package schema applies a database's migrations: an ordered list, each a
// version, a name and an Up, run once and recorded in schema_versions. A
// fresh database and a migrated one walk the same list, so they cannot end
// up differing; a step that has been recorded is never run again, so every
// Up may assume the ones before it and need not be idempotent — though the
// ones the apps carry from before this list all are.
package schema

import (
	"database/sql"
	"fmt"
)

type Migration struct {
	Version int
	Name    string
	Up      func(db *sql.DB) error
}

// Apply runs every migration whose version schema_versions does not record,
// in list order, recording each as it lands. The list must be strictly
// increasing by version — a duplicate would let one step's record hide the
// other.
func Apply(db *sql.DB, list []Migration) error {
	if _, err := db.Exec(`
create table if not exists schema_versions(
  version integer primary key,
  applied_at text not null
)`); err != nil {
		return err
	}
	last := 0
	for _, m := range list {
		if m.Version <= last {
			return fmt.Errorf("schema: migration %d (%s) is out of order after %d", m.Version, m.Name, last)
		}
		last = m.Version
		applied, err := Applied(db, m.Version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := m.Up(db); err != nil {
			return fmt.Errorf("schema: migration %d (%s): %w", m.Version, m.Name, err)
		}
		if _, err := db.Exec(`insert or ignore into schema_versions(version, applied_at) values(?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`, m.Version); err != nil {
			return err
		}
	}
	return nil
}

// Exec is an Up that runs one SQL script.
func Exec(script string) func(db *sql.DB) error {
	return func(db *sql.DB) error {
		_, err := db.Exec(script)
		return err
	}
}

// Applied reports whether schema_versions records the version.
func Applied(db *sql.DB, version int) (bool, error) {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = ?`, version).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}
