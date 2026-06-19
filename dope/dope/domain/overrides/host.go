package overrides

import (
	"context"
	"database/sql"
)

// Host is the server capability the override writes need.
type Host interface {
	WithWriteTx(reqCtx context.Context, festID int64, label string, fn func(ctx context.Context, tx *sql.Tx) error) error
}
