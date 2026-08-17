package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// ResolveFestID accepts either a positive integer (the fest id) or a slug and
// returns the numeric fest id. Returns sql.ErrNoRows if no fest matches.
func ResolveFestID(ctx context.Context, q Queryer, ref string) (int64, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return 0, sql.ErrNoRows
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil && id > 0 {
		var found int64
		if err := q.QueryRowContext(ctx, `select id from fests where id = ?`, id).Scan(&found); err != nil {
			return 0, err
		}
		return found, nil
	}
	var id int64
	if err := q.QueryRowContext(ctx, `select id from fests where slug = ?`, ref).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// FlatMatchID returns the id of the single match (code 'main') hosting a flat
// (ЧГК-family) game's state under the unified model.
func FlatMatchID(ctx context.Context, q Queryer, gameID int64) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx,
		`select id from matches where game_id = ? and code = 'main'`, gameID).Scan(&id)
	return id, err
}

// FlatGameStateJSON reads a flat game's state document from its match.
func FlatGameStateJSON(ctx context.Context, q Queryer, gameID int64) (string, error) {
	var state string
	err := q.QueryRowContext(ctx,
		`select state_json from matches where game_id = ? and code = 'main'`, gameID).Scan(&state)
	if state == "" {
		state = "{}"
	}
	return state, err
}
