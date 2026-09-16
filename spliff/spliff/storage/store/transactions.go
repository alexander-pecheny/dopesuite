package store

import (
	"context"
	"sort"
	"strconv"
)

// Entry is one Member's Payment or Share, in the Transaction's own currency.
type Entry struct {
	MemberID int64
	Minor    int64
}

// Photo is one picture attached to a Transaction. Ref addresses the bytes in
// the blob store; the size is kept so the page can lay the picture out before
// it has loaded.
type Photo struct {
	ID         int64
	Ref        string
	UploaderID int64
	CreatedAt  string
	Width      int
	Height     int
	Bytes      int64
}

// Transaction is one dated movement of money, in the currency it happened in.
type Transaction struct {
	ID          int64
	GroupID     int64
	Description string
	Day         string
	Currency    string
	TotalMinor  int64
	CreatedBy   int64
	CreatedAt   string
	UpdatedAt   string
	DeletedAt   string // "" = live
	Payments    []Entry
	Shares      []Entry
	Photos      []Photo
}

// Deleted reports whether the Transaction has left the ledger. It is still in
// History, and any Member may restore it.
func (t Transaction) Deleted() bool { return t.DeletedAt != "" }

// Unclaimed is the part of the total no Share accounts for.
func (t Transaction) Unclaimed() int64 {
	claimed := int64(0)
	for _, s := range t.Shares {
		claimed += s.Minor
	}
	if left := t.TotalMinor - claimed; left > 0 {
		return left
	}
	return 0
}

// Write is a Transaction's own fields as a write states them — everything a
// person can change, and nothing the database decides.
type Write struct {
	Description string
	Day         string
	Currency    string
	TotalMinor  int64
	Payments    []Entry
	Shares      []Entry
}

const txColumns = `t.id, t.group_id, t.description, t.day, t.currency, t.total_minor,
       t.created_by, t.created_at, t.updated_at, coalesce(t.deleted_at, '')`

func scanTx(sc interface{ Scan(...any) error }) (Transaction, error) {
	var t Transaction
	err := sc.Scan(&t.ID, &t.GroupID, &t.Description, &t.Day, &t.Currency, &t.TotalMinor,
		&t.CreatedBy, &t.CreatedAt, &t.UpdatedAt, &t.DeletedAt)
	return t, err
}

// TransactionByID reads one Transaction whole, live or deleted.
func TransactionByID(ctx context.Context, q Querier, id int64) (Transaction, error) {
	t, err := scanTx(q.QueryRowContext(ctx, `select `+txColumns+` from transactions t where t.id = ?`, id))
	if err != nil {
		return t, notFound(err)
	}
	list := []Transaction{t}
	if err := fillEntries(ctx, q, list); err != nil {
		return t, err
	}
	if err := fillPhotos(ctx, q, list); err != nil {
		return t, err
	}
	return list[0], nil
}

// GroupTransactions is a Group's Transactions, newest first. live picks the
// ledger's set; deleted ones are what History shows under it.
func GroupTransactions(ctx context.Context, q Querier, groupID int64, live bool) ([]Transaction, error) {
	filter := ` and t.deleted_at is null`
	if !live {
		filter = ` and t.deleted_at is not null`
	}
	rows, err := q.QueryContext(ctx, `
select `+txColumns+` from transactions t where t.group_id = ?`+filter+`
order by t.day desc, t.id desc`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Transaction{}
	for rows.Next() {
		t, err := scanTx(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := fillEntries(ctx, q, out); err != nil {
		return nil, err
	}
	return out, fillPhotos(ctx, q, out)
}

// fillEntries hangs every Payment and Share on its Transaction in one query a
// side, so a feed of fifty bills costs three statements and not a hundred and
// one. Both come back sorted by descending amount, which is the order the
// largest-remainder rules read payers in.
func fillEntries(ctx context.Context, q Querier, txs []Transaction) error {
	if len(txs) == 0 {
		return nil
	}
	index := make(map[int64]int, len(txs))
	args := make([]any, len(txs))
	for i, t := range txs {
		index[t.ID] = i
		args[i] = t.ID
	}
	for _, spec := range []struct {
		table string
		add   func(*Transaction, Entry)
	}{
		{"transaction_payments", func(t *Transaction, e Entry) { t.Payments = append(t.Payments, e) }},
		{"transaction_shares", func(t *Transaction, e Entry) { t.Shares = append(t.Shares, e) }},
	} {
		rows, err := q.QueryContext(ctx, `
select transaction_id, member_id, amount_minor from `+spec.table+`
where transaction_id in (`+placeholders(len(txs))+`) order by amount_minor desc, member_id`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var txID int64
			var e Entry
			if err := rows.Scan(&txID, &e.MemberID, &e.Minor); err != nil {
				rows.Close()
				return err
			}
			spec.add(&txs[index[txID]], e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func fillPhotos(ctx context.Context, q Querier, txs []Transaction) error {
	if len(txs) == 0 {
		return nil
	}
	index := make(map[int64]int, len(txs))
	args := make([]any, len(txs))
	for i, t := range txs {
		index[t.ID] = i
		args[i] = t.ID
	}
	rows, err := q.QueryContext(ctx, `
select transaction_id, id, blob_ref, uploader_id, created_at, width, height, bytes
from transaction_photos where transaction_id in (`+placeholders(len(txs))+`) order by id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var txID int64
		var p Photo
		if err := rows.Scan(&txID, &p.ID, &p.Ref, &p.UploaderID, &p.CreatedAt, &p.Width, &p.Height, &p.Bytes); err != nil {
			return err
		}
		t := &txs[index[txID]]
		t.Photos = append(t.Photos, p)
	}
	return rows.Err()
}

// InsertTransaction writes a new Transaction and its entries.
func InsertTransaction(ctx context.Context, tx Tx, groupID, actorID int64, w Write, now string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
insert into transactions(group_id, description, day, currency, total_minor, created_by, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, ?)`,
		groupID, w.Description, w.Day, w.Currency, w.TotalMinor, actorID, now, now)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, writeEntries(ctx, tx, id, w)
}

// UpdateTransaction replaces a Transaction's fields and its entries whole: an
// edit states what the Transaction IS, so a Payment somebody dropped goes away
// rather than lingering.
func UpdateTransaction(ctx context.Context, tx Tx, id int64, w Write, now string) error {
	if _, err := tx.ExecContext(ctx, `
update transactions set description = ?, day = ?, currency = ?, total_minor = ?, updated_at = ?
where id = ?`, w.Description, w.Day, w.Currency, w.TotalMinor, now, id); err != nil {
		return err
	}
	for _, table := range []string{"transaction_payments", "transaction_shares"} {
		if _, err := tx.ExecContext(ctx, `delete from `+table+` where transaction_id = ?`, id); err != nil {
			return err
		}
	}
	return writeEntries(ctx, tx, id, w)
}

func writeEntries(ctx context.Context, tx Tx, id int64, w Write) error {
	for _, e := range w.Payments {
		if _, err := tx.ExecContext(ctx, `
insert into transaction_payments(transaction_id, member_id, amount_minor) values(?, ?, ?)`,
			id, e.MemberID, e.Minor); err != nil {
			return err
		}
	}
	for _, e := range w.Shares {
		if _, err := tx.ExecContext(ctx, `
insert into transaction_shares(transaction_id, member_id, amount_minor) values(?, ?, ?)`,
			id, e.MemberID, e.Minor); err != nil {
			return err
		}
	}
	return nil
}

// SetDeleted tombstones a Transaction or brings it back. Deleting is soft in
// v1 and nothing reaps it: the rows, the Shares and the Photos all stay.
func SetDeleted(ctx context.Context, tx Tx, id int64, at string) error {
	var deleted any
	if at != "" {
		deleted = at
	}
	_, err := tx.ExecContext(ctx, `update transactions set deleted_at = ? where id = ?`, deleted, id)
	return err
}

// MemberHasEntries reports whether this member row holds a Payment or a Share
// on any LIVE Transaction of the Group. Their Net balance being zero is the
// rule that lets them go; this is the second half of it, because a pair of
// amounts that cancel is still a name the History and the feed would be left
// pointing at nobody.
func MemberHasEntries(ctx context.Context, q Querier, groupID, memberID int64) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `
select count(*) from transactions t
where t.group_id = ? and t.deleted_at is null and (
  exists(select 1 from transaction_payments p where p.transaction_id = t.id and p.member_id = ?)
  or exists(select 1 from transaction_shares s where s.transaction_id = t.id and s.member_id = ?)
)`, groupID, memberID, memberID).Scan(&n)
	return n > 0, err
}

// ---- History ----

// HistoryEntry is one change to one Transaction: who, when, what.
type HistoryEntry struct {
	ID            int64
	TransactionID int64
	ActorID       int64 // 0 = nobody (a change the server made on its own)
	At            string
	Kind          string
	Before        string
	After         string
}

// The kinds the History CHECK allows.
const (
	HistoryCreated     = "created"
	HistoryEdited      = "edited"
	HistoryDeleted     = "deleted"
	HistoryRestored    = "restored"
	HistoryPhotoAdded  = "photo_added"
	HistoryPhotoRemove = "photo_removed"
)

// AppendHistory records one change. Every write to a Transaction calls it; that
// is what makes full trust auditable.
func AppendHistory(ctx context.Context, tx Tx, txID, actorID int64, kind, before, after, now string) error {
	var actor any
	if actorID != 0 {
		actor = actorID
	}
	_, err := tx.ExecContext(ctx, `
insert into transaction_history(transaction_id, actor_id, at, kind, before_json, after_json)
values(?, ?, ?, ?, ?, ?)`, txID, actor, now, kind, nullIfEmpty(before), nullIfEmpty(after))
	return err
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// TransactionHistory is one Transaction's changes, oldest first.
func TransactionHistory(ctx context.Context, q Querier, txID int64) ([]HistoryEntry, error) {
	return historyRows(ctx, q, `where h.transaction_id = ? order by h.id`, txID)
}

// GroupHistory is the whole Group's changes, newest first — the feed a Member
// scrolls to see what everybody has been doing.
func GroupHistory(ctx context.Context, q Querier, groupID int64, limit int) ([]HistoryEntry, error) {
	if limit <= 0 {
		limit = 200
	}
	return historyRows(ctx, q, `
join transactions t on t.id = h.transaction_id
where t.group_id = ? order by h.id desc limit `+strconv.Itoa(limit), groupID)
}

func historyRows(ctx context.Context, q Querier, where string, args ...any) ([]HistoryEntry, error) {
	rows, err := q.QueryContext(ctx, `
select h.id, h.transaction_id, coalesce(h.actor_id, 0), h.at, h.kind,
       coalesce(h.before_json, ''), coalesce(h.after_json, '')
from transaction_history h `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryEntry{}
	for rows.Next() {
		var h HistoryEntry
		if err := rows.Scan(&h.ID, &h.TransactionID, &h.ActorID, &h.At, &h.Kind, &h.Before, &h.After); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ---- photos ----

// InsertPhoto records an uploaded picture against its Transaction.
func InsertPhoto(ctx context.Context, tx Tx, txID, uploaderID int64, p Photo, now string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
insert into transaction_photos(transaction_id, blob_ref, uploader_id, created_at, width, height, bytes)
values(?, ?, ?, ?, ?, ?, ?)`, txID, p.Ref, uploaderID, now, p.Width, p.Height, p.Bytes)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// PhotoByID reads one picture's row together with the Group it hangs under, so
// the Members-only check is one statement.
func PhotoByID(ctx context.Context, q Querier, id int64) (Photo, int64, error) {
	var p Photo
	var groupID int64
	err := q.QueryRowContext(ctx, `
select p.id, p.blob_ref, p.uploader_id, p.created_at, p.width, p.height, p.bytes, t.group_id
from transaction_photos p join transactions t on t.id = p.transaction_id
where p.id = ?`, id).
		Scan(&p.ID, &p.Ref, &p.UploaderID, &p.CreatedAt, &p.Width, &p.Height, &p.Bytes, &groupID)
	if err != nil {
		return p, 0, notFound(err)
	}
	return p, groupID, nil
}

// DeletePhoto drops the row and answers the blob ref, which the caller unlinks
// after the commit — a blob removed inside the transaction would be gone even
// if the transaction rolled back.
func DeletePhoto(ctx context.Context, tx Tx, id int64) (string, error) {
	var ref string
	if err := tx.QueryRowContext(ctx, `select blob_ref from transaction_photos where id = ?`, id).Scan(&ref); err != nil {
		return "", notFound(err)
	}
	_, err := tx.ExecContext(ctx, `delete from transaction_photos where id = ?`, id)
	return ref, err
}

// GroupPhotoRefs is every blob a Group's Transactions hold, for the Owner's
// delete to unlink after the rows have gone.
func GroupPhotoRefs(ctx context.Context, q Querier, groupID int64) ([]string, error) {
	rows, err := q.QueryContext(ctx, `
select p.blob_ref from transaction_photos p join transactions t on t.id = p.transaction_id
where t.group_id = ?`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// ---- rate tables ----

// RateDays is every calendar day a Rate table was fetched for, ascending.
func RateDays(ctx context.Context, q Querier) ([]string, error) {
	rows, err := q.QueryContext(ctx, `select day from rate_fetches order by day`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var day string
		if err := rows.Scan(&day); err != nil {
			return nil, err
		}
		out = append(out, day)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// RateTable is one day's rates.
func RateTable(ctx context.Context, q Querier, day string) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `select currency, rate from rate_tables where day = ?`, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var currency, rate string
		if err := rows.Scan(&currency, &rate); err != nil {
			return nil, err
		}
		out[currency] = rate
	}
	return out, rows.Err()
}

// SaveRateTable writes one day's rates and records the fetch. A day already
// fetched is overwritten wholesale, which is what a re-fetch of the same day
// means; there is no merge, because a partial table is not a Rate table.
func SaveRateTable(ctx context.Context, tx Tx, day, source string, table map[string]string, now string) error {
	if _, err := tx.ExecContext(ctx, `delete from rate_tables where day = ?`, day); err != nil {
		return err
	}
	for currency, rate := range table {
		if _, err := tx.ExecContext(ctx,
			`insert into rate_tables(day, currency, rate) values(?, ?, ?)`, day, currency, rate); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `
insert into rate_fetches(day, source, fetched_at) values(?, ?, ?)
on conflict(day) do update set source = excluded.source, fetched_at = excluded.fetched_at`,
		day, source, now)
	return err
}

// HasRateTable reports whether a day was already fetched.
func HasRateTable(ctx context.Context, q Querier, day string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `select count(*) from rate_fetches where day = ?`, day).Scan(&n)
	return n > 0, err
}
