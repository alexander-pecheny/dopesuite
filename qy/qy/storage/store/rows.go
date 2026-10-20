package store

import (
	"context"
	"database/sql"
)

// Collect runs query and scans each row with scan, the loop every list read
// would otherwise write out again.
func Collect[T any](ctx context.Context, q Querier, scan func(*sql.Rows) (T, error), query string, args ...any) ([]T, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Scalar scans a one-column row.
func Scalar[T any](rows *sql.Rows) (T, error) {
	var v T
	err := rows.Scan(&v)
	return v, err
}

// Pair is a two-column row: an id and a number about it.
type Pair struct {
	ID int64
	N  int
}

func ScanPair(rows *sql.Rows) (Pair, error) {
	var p Pair
	err := rows.Scan(&p.ID, &p.N)
	return p, err
}

// Counts reads id-and-number rows into a map.
func Counts(ctx context.Context, q Querier, query string, args ...any) (map[int64]int, error) {
	pairs, err := Collect(ctx, q, ScanPair, query, args...)
	out := make(map[int64]int, len(pairs))
	for _, p := range pairs {
		out[p.ID] = p.N
	}
	return out, err
}
