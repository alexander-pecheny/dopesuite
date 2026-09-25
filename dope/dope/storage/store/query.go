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

// RowQueryer is the single-row read surface of *sql.DB, *sql.Tx and the
// handshake's transaction.
type RowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// UserIDByName finds the account whose username is name, ignoring case; the
// users_username_nocase index keeps that to one. sql.ErrNoRows when there is
// none.
func UserIDByName(ctx context.Context, q RowQueryer, name string) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `select id from users where username = ? collate nocase`, name).Scan(&id)
	return id, err
}

// UserIDByTelegramName is UserIDByName for the Telegram handle. Handles are
// not unique here, so where two differ only by case the exact spelling wins,
// then the older account.
func UserIDByTelegramName(ctx context.Context, q RowQueryer, name string) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `
select id from users where telegram_username = ? collate nocase
order by telegram_username = ? desc, id limit 1`, name, name).Scan(&id)
	return id, err
}
