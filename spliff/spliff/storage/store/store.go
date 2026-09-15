package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Querier is any read handle — the pool or a transaction.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Tx is the write transaction the server opens and bounds.
type Tx interface {
	Querier
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// ---- groups ----

// Group is one circle of people who share expenses.
type Group struct {
	ID           int64
	Name         string
	BaseCurrency string
	OwnerID      int64
	CreatedAt    string
}

// Member is one person's place in a Group. Name is what the Group calls them:
// their username, else the telegram one they arrived with.
type Member struct {
	UserID   int64
	Name     string
	JoinedAt string
	IsOwner  bool
}

// CreateGroup writes the Group and seats its Owner as its first Member, so join
// order starts with the person who made it.
func CreateGroup(ctx context.Context, tx Tx, name, currency string, ownerID int64, now string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
insert into groups(name, base_currency, owner_id, created_at) values(?, ?, ?, ?)`,
		name, currency, ownerID, now)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, AddMember(ctx, tx, id, ownerID, now)
}

// GroupByID reads one Group; sql.ErrNoRows when it is gone.
func GroupByID(ctx context.Context, q Querier, id int64) (Group, error) {
	var g Group
	err := q.QueryRowContext(ctx, `
select id, name, base_currency, owner_id, created_at from groups where id = ?`, id).
		Scan(&g.ID, &g.Name, &g.BaseCurrency, &g.OwnerID, &g.CreatedAt)
	return g, err
}

// GroupsOf is every Group this person belongs to, newest first.
func GroupsOf(ctx context.Context, q Querier, userID int64) ([]Group, error) {
	rows, err := q.QueryContext(ctx, `
select g.id, g.name, g.base_currency, g.owner_id, g.created_at
from groups g join group_members m on m.group_id = g.id
where m.user_id = ? order by g.id desc`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.BaseCurrency, &g.OwnerID, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetGroupName renames a Group. Every Member may.
func SetGroupName(ctx context.Context, tx Tx, groupID int64, name string) error {
	_, err := tx.ExecContext(ctx, `update groups set name = ? where id = ?`, name, groupID)
	return err
}

// SetBaseCurrency restates every balance and rewrites nothing.
func SetBaseCurrency(ctx context.Context, tx Tx, groupID int64, currency string) error {
	_, err := tx.ExecContext(ctx, `update groups set base_currency = ? where id = ?`, currency, groupID)
	return err
}

// SetOwner hands the Group on. The old Owner stays a Member.
func SetOwner(ctx context.Context, tx Tx, groupID, userID int64) error {
	_, err := tx.ExecContext(ctx, `update groups set owner_id = ? where id = ?`, userID, groupID)
	return err
}

// DeleteGroup destroys the Group and everything hanging off it. The Owner may
// only reach this once every balance is zero, so nothing owed is being thrown
// away; the photo blobs are unlinked by the caller, which knows the store.
func DeleteGroup(ctx context.Context, tx Tx, groupID int64) error {
	_, err := tx.ExecContext(ctx, `delete from groups where id = ?`, groupID)
	return err
}

// Members is the Group's people in join order — joined_at, then id, which is
// the tie-break the Debt graph and every derived split are sorted by.
func Members(ctx context.Context, q Querier, groupID int64) ([]Member, error) {
	rows, err := q.QueryContext(ctx, `
select m.user_id, coalesce(nullif(u.username, ''), u.telegram_username, ''), m.joined_at,
       (g.owner_id = m.user_id)
from group_members m
join users u on u.id = m.user_id
join groups g on g.id = m.group_id
where m.group_id = ? order by m.joined_at, m.id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Name, &m.JoinedAt, &m.IsOwner); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MemberIDs is Members reduced to the join-ordered id list the ledger takes.
func MemberIDs(members []Member) []int64 {
	out := make([]int64, len(members))
	for i, m := range members {
		out[i] = m.UserID
	}
	return out
}

// IsMember reports whether this person is in the Group.
func IsMember(ctx context.Context, q Querier, groupID, userID int64) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx,
		`select count(*) from group_members where group_id = ? and user_id = ?`, groupID, userID).Scan(&n)
	return n > 0, err
}

// AddMember seats somebody, idempotently: two people racing the last seat on an
// Invite Link must not fail the second writer with a unique violation.
func AddMember(ctx context.Context, tx Tx, groupID, userID int64, now string) error {
	_, err := tx.ExecContext(ctx, `
insert into group_members(group_id, user_id, joined_at) values(?, ?, ?)
on conflict(group_id, user_id) do nothing`, groupID, userID, now)
	return err
}

// RemoveMember takes somebody out. The caller has already proved their Net
// balance is zero — nobody leaves owing, so the Debt graph never names a ghost.
func RemoveMember(ctx context.Context, tx Tx, groupID, userID int64) error {
	_, err := tx.ExecContext(ctx,
		`delete from group_members where group_id = ? and user_id = ?`, groupID, userID)
	return err
}

// UserNames is how the invite list writes a person: username, else the telegram
// one they arrived with.
func UserNames(ctx context.Context, q Querier, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := q.QueryContext(ctx, `
select id, coalesce(nullif(username, ''), telegram_username, '') from users
where id in (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// TelegramOf is the telegram account ids of the people named, for the three DMs
// the app sends. Somebody who logged in with a password has none, and simply
// does not get knocked on.
func TelegramOf(ctx context.Context, q Querier, ids []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := q.QueryContext(ctx, `
select id, telegram_user_id from users
where telegram_user_id is not null and id in (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, tg int64
		if err := rows.Scan(&id, &tg); err != nil {
			return nil, err
		}
		out[id] = tg
	}
	return out, rows.Err()
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

// ErrNotFound is what a read answers for a row that is not there — the server's
// edge turns it into a 404 in its own words.
var ErrNotFound = errors.New("store: not found")

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
