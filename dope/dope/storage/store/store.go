// Package store is the SQLite persistence layer. It is the eventual home for
// the schema, queries and shared view types currently in package main's db.go
// (ARCHITECTURE.md roadmap item 5, "the biggest and last step, because almost
// everything depends on it").
//
// This first slice holds the self-contained connection plumbing: the DSN
// builder, the read-pool size, and the additive schema-evolution helper. These
// depend only on database/sql and the standard library — no server, view types
// or HTTP — so they form a clean leaf the rest of the store can grow onto.
package store

import (
	"database/sql"
	"fmt"
	"sort"

	"pecheny.me/dopecore/sqlitex"

	// Blank-imported so its init() registers the dope_z/dope_unz scalar SQL
	// functions (audit-log zstd compression) on the shared sqlite driver. The
	// functions are invoked only from SQL (audit triggers), so this package has
	// no Go-level dependency on sqlitez; the import exists purely to run that
	// registration whenever the store package is used. sqlitez imports nothing
	// from store, so there is no cycle.
	_ "dope/dope/storage/sqlitez"
)

// MaxOpenConns sizes the read connection pool. SQLite under WAL handles many
// concurrent readers against a single writer; this lets viewer GETs and SSE
// bootstraps proceed in parallel with host edits.
const MaxOpenConns = sqlitex.MaxOpenConns

// BuildDSN turns a bare file path into a URI that ships the shared pragma set
// with every new pool connection.
func BuildDSN(path string) string { return sqlitex.BuildDSN(path) }

// ColumnSpec is one column to add to a table in an additive migration.
type ColumnSpec struct {
	Name string
	Type string
}

// TableShape returns a table's column names (in cid order) and its primary-key
// columns (in key order), via pragma_table_info. table is interpolated, so it
// must come from a trusted source (a hard-coded table list), not user input.
func TableShape(db *sql.DB, table string) (cols, pks []string, err error) {
	rows, err := db.Query(`select name, pk from pragma_table_info('` + table + `') order by cid`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	type pkCol struct {
		name string
		rank int
	}
	var pkCols []pkCol
	for rows.Next() {
		var name string
		var pk int
		if err := rows.Scan(&name, &pk); err != nil {
			return nil, nil, err
		}
		cols = append(cols, name)
		if pk > 0 {
			pkCols = append(pkCols, pkCol{name: name, rank: pk})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	sort.SliceStable(pkCols, func(i, j int) bool { return pkCols[i].rank < pkCols[j].rank })
	for _, p := range pkCols {
		pks = append(pks, p.name)
	}
	return cols, pks, nil
}

// ColumnExists reports whether a table has a column (false if the table is
// absent). Used to decide whether an early-adopter table needs reshaping.
func ColumnExists(db *sql.DB, table, column string) bool {
	rows, err := db.Query(`select name from pragma_table_info(?)`, table)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}

// AddColumnsIfMissing adds each column to the table if it is not already
// present — the additive half of the schema migrations.
func AddColumnsIfMissing(db *sql.DB, table string, columns []ColumnSpec) error {
	rows, err := db.Query(`select name from pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	existing := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, col := range columns {
		if existing[col.Name] {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf("alter table %s add column %s %s", table, col.Name, col.Type)); err != nil {
			return err
		}
	}
	return nil
}
