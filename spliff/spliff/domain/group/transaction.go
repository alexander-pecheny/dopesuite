package group

import (
	"context"
	"strings"
	"time"

	corei18n "pecheny.me/dopecore/i18nstrings"

	"spliff/spliff/domain/money"
	"spliff/spliff/domain/rates"
	"spliff/spliff/storage/store"

	spliffstrings "spliff/i18nstrings"
)

// MaxDescriptionRunes bounds a Transaction's description. It is free text, not
// a story: the feed shows it on one line.
const MaxDescriptionRunes = 200

// Every write to a Transaction goes through this file: validate it against the
// Members as they are inside the transaction, write it, and append to History.
// The validation is not the form's. A Share that overdraws the total is
// refused here whatever typed it, because otherwise the ledger's zero-sum
// assertion would break, and it would break for everybody in the Group at once.

// Entry is one Payment or Share as somebody typed it.
type Entry struct {
	MemberID int64
	Minor    int64
}

// Request is a Transaction as a write states it, before it is checked.
type Request struct {
	Description string
	Day         string
	Currency    string
	TotalMinor  int64
	Payments    []Entry
	Shares      []Entry
}

// Record checks a new Transaction against the Group's current Members and
// writes it with its History entry. It answers the new Transaction's id.
func Record(ctx context.Context, tx store.Tx, groupID, actorID int64, req Request, now string) (int64, error) {
	members, err := store.Members(ctx, tx, groupID)
	if err != nil {
		return 0, err
	}
	write, err := validate(req, members)
	if err != nil {
		return 0, err
	}
	id, err := store.InsertTransaction(ctx, tx, groupID, actorID, write, now)
	if err != nil {
		return 0, err
	}
	return id, store.AppendHistory(ctx, tx, id, actorID,
		store.HistoryCreated, "", snapshotOfWrite(write, 0), now)
}

// Edit replaces what a Transaction IS. The "before" that History keeps is read
// in this transaction, so two edits that race each record the other's result
// and not what both of them started from.
func Edit(ctx context.Context, tx store.Tx, groupID, txID, actorID int64, req Request, now string) error {
	before, err := store.TransactionByID(ctx, tx, txID)
	if err != nil {
		return err
	}
	if before.Deleted() {
		return corei18n.User(spliffstrings.Default.Transaction.Error.Deleted())
	}
	members, err := store.Members(ctx, tx, groupID)
	if err != nil {
		return err
	}
	write, err := validate(req, members)
	if err != nil {
		return err
	}
	if err := store.UpdateTransaction(ctx, tx, txID, write, now); err != nil {
		return err
	}
	return store.AppendHistory(ctx, tx, txID, actorID, store.HistoryEdited,
		snapshotOf(before), snapshotOfWrite(write, len(before.Photos)), now)
}

// SetDeleted tombstones a Transaction or restores one. A deleted Transaction
// leaves the ledger and stays in History. It answers false when the
// Transaction was already in the asked state, and then writes nothing.
//
// A restore puts the Transaction back into the ledger, so it is held to the
// rule a new one is: everybody it names must still be a Member. Somebody may
// have left while it was deleted, because a deleted Transaction does not hold
// anybody in the Group.
func SetDeleted(ctx context.Context, tx store.Tx, groupID, txID, actorID int64, deleted bool, now string) (bool, error) {
	before, err := store.TransactionByID(ctx, tx, txID)
	if err != nil {
		return false, err
	}
	if before.Deleted() == deleted {
		return false, nil
	}
	at, kind := now, store.HistoryDeleted
	if !deleted {
		at, kind = "", store.HistoryRestored
		if err := checkStillMembers(ctx, tx, groupID, before); err != nil {
			return false, err
		}
	}
	if err := store.SetDeleted(ctx, tx, txID, at); err != nil {
		return false, err
	}
	return true, store.AppendHistory(ctx, tx, txID, actorID, kind, snapshotOf(before), "", now)
}

func checkStillMembers(ctx context.Context, tx store.Tx, groupID int64, t store.Transaction) error {
	members, err := store.Members(ctx, tx, groupID)
	if err != nil {
		return err
	}
	current := memberRowIDs(members)
	for _, side := range [][]store.Entry{t.Payments, t.Shares} {
		for _, e := range side {
			if !current[e.MemberID] {
				return corei18n.User(spliffstrings.Default.Transaction.Error.NamesFormerMember())
			}
		}
	}
	return nil
}

// AttachPhoto records a Photo whose blob is already stored, with its History
// entry, and answers the Photo's id.
func AttachPhoto(ctx context.Context, tx store.Tx, txID, actorID int64, photo store.Photo, now string) (int64, error) {
	t, err := store.TransactionByID(ctx, tx, txID)
	if err != nil {
		return 0, err
	}
	id, err := store.InsertPhoto(ctx, tx, txID, actorID, photo, now)
	if err != nil {
		return 0, err
	}
	return id, store.AppendHistory(ctx, tx, txID, actorID,
		store.HistoryPhotoAdded, "", snapshotOf(withPhotoCount(t, len(t.Photos)+1)), now)
}

// DetachPhoto drops a Photo's row with its History entry. It answers the
// Transaction the Photo hung on, and the blob ref for the caller to unlink
// after the commit.
func DetachPhoto(ctx context.Context, tx store.Tx, photoID, actorID int64, now string) (int64, string, error) {
	txID, err := store.PhotoTransaction(ctx, tx, photoID)
	if err != nil {
		return 0, "", err
	}
	t, err := store.TransactionByID(ctx, tx, txID)
	if err != nil {
		return 0, "", err
	}
	ref, err := store.DeletePhoto(ctx, tx, photoID)
	if err != nil {
		return 0, "", err
	}
	return txID, ref, store.AppendHistory(ctx, tx, txID, actorID, store.HistoryPhotoRemove,
		snapshotOf(t), snapshotOf(withPhotoCount(t, len(t.Photos)-1)), now)
}

// ---- the rules ----

// validateHeader checks everything but the entries, and returns the
// description and currency in their stored form.
func validateHeader(req Request) (description, currency string, err error) {
	str := spliffstrings.Default
	description = strings.TrimSpace(req.Description)
	if description == "" {
		return "", "", corei18n.User(str.Transaction.Error.DescriptionRequired())
	}
	if len([]rune(description)) > MaxDescriptionRunes {
		return "", "", corei18n.User(str.Transaction.Error.DescriptionTooLong())
	}
	if _, err := time.Parse(rates.DayFormat, req.Day); err != nil {
		return "", "", corei18n.User(str.Transaction.Error.DayInvalid())
	}
	currency = money.Normalise(req.Currency)
	if !money.ValidCode(currency) {
		return "", "", corei18n.User(str.Transaction.Error.CurrencyInvalid())
	}
	if req.TotalMinor <= 0 {
		return "", "", corei18n.User(str.Transaction.Error.TotalPositive())
	}
	return description, currency, nil
}

// memberRowIDs is the set every Payment and Share must name from: current
// MEMBER ROWS, which is what lets a Phantom hold one.
func memberRowIDs(members []store.Member) map[int64]bool {
	current := map[int64]bool{}
	for _, m := range members {
		current[m.ID] = true
	}
	return current
}

// validate is the whole rule set, in the order a person would hit it. Every
// refusal is a User Error naming the number that is wrong, because "the shares
// do not add up" sends somebody hunting and "12.00 is left to claim" does not.
func validate(req Request, members []store.Member) (store.Write, error) {
	str := spliffstrings.Default
	description, currency, err := validateHeader(req)
	if err != nil {
		return store.Write{}, err
	}

	current := memberRowIDs(members)
	payments, paid, err := checkEntries(req.Payments, current, false)
	if err != nil {
		return store.Write{}, err
	}
	if paid != req.TotalMinor {
		return store.Write{}, corei18n.User(str.Transaction.Error.PaymentsMismatch(
			money.Format(paid, currency), money.Format(req.TotalMinor, currency)+" "+currency))
	}
	shares, claimed, err := checkEntries(req.Shares, current, true)
	if err != nil {
		return store.Write{}, err
	}
	if claimed > req.TotalMinor {
		return store.Write{}, corei18n.User(str.Transaction.Error.SharesOverdraw(
			money.Format(claimed-req.TotalMinor, currency) + " " + currency))
	}
	return store.Write{
		Description: description, Day: req.Day, Currency: currency,
		TotalMinor: req.TotalMinor, Payments: payments, Shares: shares,
	}, nil
}

// checkEntries turns one side's rows into entries: one per Member at most, no
// negative amount, and every name a current Member. A zero row is dropped
// rather than refused. A form that lists everybody and leaves some at nothing
// is exactly how an even split across four of six people is typed.
func checkEntries(in []Entry, current map[int64]bool, allowEmpty bool) ([]store.Entry, int64, error) {
	str := spliffstrings.Default
	seen := map[int64]bool{}
	out := []store.Entry{}
	total := int64(0)
	for _, e := range in {
		if !current[e.MemberID] {
			return nil, 0, corei18n.User(str.Transaction.Error.NotAMember())
		}
		if seen[e.MemberID] {
			return nil, 0, corei18n.User(str.Transaction.Error.DuplicateMember())
		}
		seen[e.MemberID] = true
		if e.Minor < 0 {
			return nil, 0, corei18n.User(str.Transaction.Error.NegativeAmount())
		}
		if e.Minor == 0 {
			continue
		}
		out = append(out, store.Entry{MemberID: e.MemberID, Minor: e.Minor})
		total += e.Minor
	}
	if len(out) == 0 && !allowEmpty {
		return nil, 0, corei18n.User(str.Transaction.Error.PaymentRequired())
	}
	return out, total, nil
}
