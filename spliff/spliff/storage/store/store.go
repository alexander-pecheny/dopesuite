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

// Member is one person's place in a Group. ID is the group_members row, and it
// is the identity every Payment and Share names — a Phantom has no account, so
// a user id could not serve. Name is what the Group calls them: their username,
// else the telegram one they arrived with, else the name the Owner gave the
// Phantom.
type Member struct {
	ID       int64
	UserID   int64 // 0 for a Phantom
	Name     string
	JoinedAt string
	IsOwner  bool
}

// IsPhantom reports whether this Member has no account behind it.
func (m Member) IsPhantom() bool { return m.UserID == 0 }

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
	return memberRows(ctx, q, `where m.group_id = ? order by m.joined_at, m.id`, groupID)
}

// MemberByID reads one Member of one Group; ErrNotFound when the row is not
// there or belongs to another Group.
func MemberByID(ctx context.Context, q Querier, groupID, memberID int64) (Member, error) {
	list, err := memberRows(ctx, q, `where m.group_id = ? and m.id = ?`, groupID, memberID)
	if err != nil {
		return Member{}, err
	}
	if len(list) == 0 {
		return Member{}, ErrNotFound
	}
	return list[0], nil
}

// Phantoms is the Group's Members with no account, in join order — what a join
// page offers as "I am …".
func Phantoms(ctx context.Context, q Querier, groupID int64) ([]Member, error) {
	return memberRows(ctx, q,
		`where m.group_id = ? and m.user_id is null order by m.joined_at, m.id`, groupID)
}

// memberRows is the one shape a Member is read in: join order is joined_at then
// id, which is the tie-break the Debt graph and every derived split are sorted
// by — and it is the member ROW's join time whether or not an account is behind
// it, so a claimed Phantom keeps the place it has always had.
//
// The join to users is a LEFT join because a Phantom has none, and the name
// falls back through the account's two names to the one the Owner typed.
func memberRows(ctx context.Context, q Querier, where string, args ...any) ([]Member, error) {
	rows, err := q.QueryContext(ctx, `
select m.id, coalesce(m.user_id, 0),
       coalesce(nullif(u.username, ''), nullif(u.telegram_username, ''), m.display_name, ''),
       m.joined_at,
       (m.user_id is not null and g.owner_id = m.user_id)
from group_members m
left join users u on u.id = m.user_id
join groups g on g.id = m.group_id
`+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.UserID, &m.Name, &m.JoinedAt, &m.IsOwner); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MemberIDs is Members reduced to the join-ordered id list the ledger takes —
// member rows, not accounts.
func MemberIDs(members []Member) []int64 {
	out := make([]int64, len(members))
	for i, m := range members {
		out[i] = m.ID
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

// RemoveMember takes one member row out, Phantom or person alike. The caller
// has already proved their Net balance is zero — nobody leaves owing, so the
// Debt graph never names a ghost.
func RemoveMember(ctx context.Context, tx Tx, groupID, memberID int64) error {
	_, err := tx.ExecContext(ctx,
		`delete from group_members where group_id = ? and id = ?`, groupID, memberID)
	return err
}

// AddPhantom seats somebody who is not on Spliff: a member row with a name and
// no account. Everything else about it is a Member — it pays, it holds Shares,
// it is in the Debt graph, and it cannot be removed while it owes anything.
func AddPhantom(ctx context.Context, tx Tx, groupID int64, name, now string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
insert into group_members(group_id, user_id, display_name, joined_at) values(?, null, ?, ?)`,
		groupID, name, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ClaimPhantom gives a Phantom's member row to the account that just joined. It
// is one UPDATE of one row on purpose: the Payments, the Shares and the History
// already name that row, so they are re-pointed by it and nothing else moves —
// every balance in the Group is the same before and after.
//
// The `user_id is null` guard makes it safe to race: the second writer claims
// nothing and is told so.
func ClaimPhantom(ctx context.Context, tx Tx, groupID, memberID, userID int64) error {
	res, err := tx.ExecContext(ctx, `
update group_members set user_id = ?, display_name = null
where id = ? and group_id = ? and user_id is null`, userID, memberID, groupID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
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
