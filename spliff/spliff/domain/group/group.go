// Package group holds a Group's rules: who may be named on a Transaction, who
// may leave, and what History records about each change.
//
// Every write here takes the write transaction it runs in, and reads what its
// rule depends on (the Members, the Net balances, the Transaction as it stood)
// through that same transaction. A rule checked on the pool before the write
// lock is taken can be broken by the write that commits in between: a Member
// removed while somebody records a bill naming them, or an edit that records a
// stale "before" in History. Inside the transaction nothing can come between
// the check and the write.
//
// Unlike money, rates, split and ledger, this package is not pure: it reads
// and writes through storage/store. It refuses with User Errors in the
// Catalog's words, so the HTTP layer only decodes, opens the transaction and
// encodes.
package group

import (
	"context"
	"errors"
	"strings"

	corei18n "pecheny.me/dopecore/i18nstrings"

	"spliff/spliff/domain/ledger"
	"spliff/spliff/domain/money"
	"spliff/spliff/domain/rates"
	"spliff/spliff/storage/store"

	spliffstrings "spliff/i18nstrings"
)

// MaxNameRunes bounds a Group's name: long enough for "Tbilisi trip, October",
// short enough that the Groups list stays a list.
const MaxNameRunes = 80

// MaxPhantomNameRunes bounds the name the Owner gives a Phantom. It is a
// person's name at a table, not a description.
const MaxPhantomNameRunes = 60

// Rates is the Rate table a Transaction dated `day` converts with. The server's
// Book answers it. The Book is loaded before the write transaction opens,
// because loading it may fetch today's table over the network, and that must
// never happen while the write lock is held.
type Rates interface {
	For(day string) (rates.Table, error)
	Empty() bool
}

// Balances is the ledger over a Group's live Transactions: every Member's Net
// balance in the Base currency, in join order. With no Rate table at all it
// answers the Members and rates.ErrNoTable.
func Balances(ctx context.Context, q store.Querier, g store.Group, book Rates) ([]ledger.Balance, []store.Member, error) {
	members, err := store.Members(ctx, q, g.ID)
	if err != nil {
		return nil, nil, err
	}
	txs, err := store.GroupTransactions(ctx, q, g.ID, true)
	if err != nil {
		return nil, members, err
	}
	if book.Empty() {
		return nil, members, rates.ErrNoTable
	}
	entries := make([]ledger.Transaction, 0, len(txs))
	for _, t := range txs {
		table, err := book.For(t.Day)
		if err != nil {
			return nil, members, err
		}
		entries = append(entries, ledgerTx(t, table))
	}
	balances, err := ledger.Balances(g.BaseCurrency, store.MemberIDs(members), entries)
	return balances, members, err
}

// ledgerTx is a stored Transaction as the ledger reads it. v1 stores no Pinned
// rate, so the pin is always nil. The argument is there so that the day a pin
// is stored, this is the one line that changes.
func ledgerTx(t store.Transaction, table rates.Table) ledger.Transaction {
	out := ledger.Transaction{
		ID: t.ID, Currency: t.Currency, Total: t.TotalMinor, Table: table, Pin: nil,
	}
	for _, e := range t.Payments {
		out.Payments = append(out.Payments, ledger.Entry{MemberID: e.MemberID, Minor: e.Minor})
	}
	for _, e := range t.Shares {
		out.Shares = append(out.Shares, ledger.Entry{MemberID: e.MemberID, Minor: e.Minor})
	}
	return out
}

// ---- the Group itself ----

// CheckName trims a Group's name and refuses an empty or overlong one.
func CheckName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", corei18n.User(spliffstrings.Default.Group.Error.NameRequired())
	}
	if len([]rune(name)) > MaxNameRunes {
		return "", corei18n.User(spliffstrings.Default.Group.Error.NameTooLong())
	}
	return name, nil
}

// Create writes a Group and seats its Owner as the first Member. The currency
// has already been checked against the Rate tables.
func Create(ctx context.Context, tx store.Tx, rawName, currency string, ownerID int64, now string) (int64, error) {
	name, err := CheckName(rawName)
	if err != nil {
		return 0, err
	}
	return store.CreateGroup(ctx, tx, name, currency, ownerID, now)
}

// Patch renames the Group, restates it in another Base currency, or both. An
// empty argument leaves that field alone. Every Member may do both.
func Patch(ctx context.Context, tx store.Tx, groupID int64, name, currency string) error {
	if name != "" {
		if err := store.SetGroupName(ctx, tx, groupID, name); err != nil {
			return err
		}
	}
	if currency != "" {
		return store.SetBaseCurrency(ctx, tx, groupID, currency)
	}
	return nil
}

// HandOver gives the Group to another Member. userID is an account, not a
// member row: a Phantom has nobody to hand a Group to.
func HandOver(ctx context.Context, tx store.Tx, groupID, userID int64) error {
	member, err := store.IsMember(ctx, tx, groupID, userID)
	if err != nil {
		return err
	}
	if !member {
		return corei18n.User(spliffstrings.Default.Group.Error.NotAMember())
	}
	return store.SetOwner(ctx, tx, groupID, userID)
}

// Delete destroys a Group once every Net balance is zero, and answers the blob
// refs its Photos held. The caller unlinks them after the commit, because a
// blob removed inside the transaction would be gone even if it rolled back.
func Delete(ctx context.Context, tx store.Tx, book Rates, groupID int64) ([]string, error) {
	g, err := store.GroupByID(ctx, tx, groupID)
	if err != nil {
		return nil, err
	}
	balances, members, err := Balances(ctx, tx, g, book)
	if err != nil && !errors.Is(err, rates.ErrNoTable) {
		return nil, err
	}
	for _, m := range members {
		if minor := balanceOf(balances, m.ID); minor != 0 {
			return nil, notSettled(g, m, minor)
		}
	}
	refs, err := store.GroupPhotoRefs(ctx, tx, groupID)
	if err != nil {
		return nil, err
	}
	return refs, store.DeleteGroup(ctx, tx, groupID)
}

// ---- Members ----

// AddPhantom seats somebody who is not on Spliff and answers the member row.
func AddPhantom(ctx context.Context, tx store.Tx, groupID int64, rawName, now string) (int64, error) {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return 0, corei18n.User(spliffstrings.Default.Group.Error.PhantomNameRequired())
	}
	if len([]rune(name)) > MaxPhantomNameRunes {
		return 0, corei18n.User(spliffstrings.Default.Group.Error.PhantomNameTooLong())
	}
	return store.AddPhantom(ctx, tx, groupID, name, now)
}

// Leave takes the caller's own member row out, under RemoveMember's rules.
func Leave(ctx context.Context, tx store.Tx, book Rates, groupID, userID int64) error {
	members, err := store.Members(ctx, tx, groupID)
	if err != nil {
		return err
	}
	for _, m := range members {
		if m.UserID == userID {
			return RemoveMember(ctx, tx, book, groupID, m.ID)
		}
	}
	return corei18n.User(spliffstrings.Default.Group.Error.NotAMember())
}

// RemoveMember takes one member row out, Phantom or person alike. Nobody goes
// while their Net balance is non-zero, and the Owner cannot go at all without
// handing the Group on first. A Member who is level but still named on a live
// Transaction stays too, because the Debt graph and the feed must never name
// somebody who is not there.
//
// Who may remove whom (the Owner anybody, everybody themselves) is the
// caller's check: it is a question about the request, not about the Group.
func RemoveMember(ctx context.Context, tx store.Tx, book Rates, groupID, memberID int64) error {
	str := spliffstrings.Default
	g, err := store.GroupByID(ctx, tx, groupID)
	if err != nil {
		return err
	}
	member, err := store.MemberByID(ctx, tx, groupID, memberID)
	if errors.Is(err, store.ErrNotFound) {
		return corei18n.User(str.Group.Error.NotAMember())
	}
	if err != nil {
		return err
	}
	if member.IsOwner {
		return corei18n.User(str.Group.Error.OwnerMustHandOver())
	}
	if err := checkLevel(ctx, tx, book, g, member); err != nil {
		return err
	}
	return store.RemoveMember(ctx, tx, groupID, memberID)
}

// checkLevel refuses a Member whose Net balance is not zero, or who is still
// named on a live Transaction.
func checkLevel(ctx context.Context, tx store.Tx, book Rates, g store.Group, member store.Member) error {
	balances, _, err := Balances(ctx, tx, g, book)
	if err != nil && !errors.Is(err, rates.ErrNoTable) {
		return err
	}
	if minor := balanceOf(balances, member.ID); minor != 0 {
		return notSettled(g, member, minor)
	}
	named, err := store.MemberHasEntries(ctx, tx, g.ID, member.ID)
	if err != nil {
		return err
	}
	if named {
		return corei18n.User(spliffstrings.Default.Group.Error.StillNamed(member.Name))
	}
	return nil
}

func balanceOf(balances []ledger.Balance, memberID int64) int64 {
	for _, b := range balances {
		if b.MemberID == memberID {
			return b.Minor
		}
	}
	return 0
}

// notSettled names the amount, because "you cannot do this" without a number
// sends somebody hunting.
func notSettled(g store.Group, m store.Member, minor int64) error {
	return corei18n.User(spliffstrings.Default.Group.Error.NotSettled(
		m.Name, money.Format(minor, g.BaseCurrency)+" "+g.BaseCurrency))
}
