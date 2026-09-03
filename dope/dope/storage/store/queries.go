package store

import (
	"context"
	"database/sql"
	"errors"
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
// (ChGK-family) game's state under the unified model.
func FlatMatchID(ctx context.Context, q Queryer, gameID int64) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx,
		`select id from matches where game_id = ? and code = 'main'`, gameID).Scan(&id)
	return id, err
}

// EnsureParticipantByNumber finds or mints the fest's Participant that plays
// under this number — a team or a player, per roster — and keeps its display
// name and city in step with what the caller knows. The number is the
// identity (ADR-0009): two same-named teams stay distinct, and re-seeding
// follows a team across a rename.
func EnsureParticipantByNumber(ctx context.Context, tx *sql.Tx, festID int64, roster string, number int64, name, city string) (int64, error) {
	name = strings.TrimSpace(name)
	city = strings.TrimSpace(city)
	if number <= 0 || name == "" {
		return 0, errors.New("a Participant needs a number and a name")
	}
	var id int64
	var oldName, oldCity string
	err := tx.QueryRowContext(ctx, `
select id, name, city from participants
where fest_id = ? and roster = ? and number = ? limit 1`, festID, roster, number).Scan(&id, &oldName, &oldCity)
	if errors.Is(err, sql.ErrNoRows) {
		return InsertReturningID(ctx, tx, `
insert into participants(fest_id, roster, name, city, number) values(?, ?, ?, ?, ?)`, festID, roster, name, city, number)
	}
	if err != nil {
		return 0, err
	}
	if city == "" {
		city = oldCity
	}
	if name != oldName || city != oldCity {
		if _, err := tx.ExecContext(ctx, `update participants set name = ?, city = ? where id = ?`, name, city, id); err != nil {
			return 0, err
		}
	}
	return id, nil
}
