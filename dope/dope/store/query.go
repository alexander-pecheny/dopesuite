package store

import (
	"context"
	"database/sql"
)

// Queryer is the read surface shared by *sql.DB and *sql.Tx, so query helpers
// work against either a pooled connection or an open transaction.
type Queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// CollectRows runs a query and maps each row with scan, returning the slice.
// It centralises the rows.Next/Scan/Close/Err boilerplate the query layer
// repeats everywhere.
func CollectRows[T any](ctx context.Context, q Queryer, query string, args []any, scan func(*sql.Rows) (T, error)) ([]T, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []T
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// InsertReturningID executes an INSERT on tx and returns the new rowid.
func InsertReturningID(ctx context.Context, tx *sql.Tx, query string, args ...any) (int64, error) {
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}
