package server

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// patch collects the columns a PATCH touched and writes them as one UPDATE,
// stamping updated_at once (labels carry none). An empty patch writes nothing.
type patch struct {
	sets []string
	args []any
}

func (p *patch) set(col string, v any) {
	p.sets = append(p.sets, col+" = ?")
	p.args = append(p.args, v)
}

func (p *patch) apply(ctx context.Context, tx *sql.Tx, table string, id int64) error {
	if len(p.sets) == 0 {
		return nil
	}
	if table != "labels" {
		p.set("updated_at", rfc3339(time.Now()))
	}
	_, err := tx.ExecContext(ctx, `update `+table+` set `+strings.Join(p.sets, ", ")+` where id = ?`, append(p.args, id)...)
	return err
}
