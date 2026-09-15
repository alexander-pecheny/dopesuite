package spliffserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	corei18n "pecheny.me/dopecore/i18nstrings"

	"spliff/spliff/domain/money"
	"spliff/spliff/domain/rates"
	"spliff/spliff/storage/store"
	"spliff/spliff/web/route"

	spliffstrings "spliff/i18nstrings"
)

// MaxDescriptionRunes bounds a Transaction's description. It is free text, not
// a story: the feed shows it on one line.
const MaxDescriptionRunes = 200

// Every write to a Transaction goes through one door: validate, write, append
// History, then knock on the doors the three DM rules name. The validation is
// not the form's — a Share that overdraws the total is refused here whatever
// typed it, because the ledger's zero-sum assertion is what would otherwise
// break, and it would break for everybody in the Group at once.

type entryRequest struct {
	MemberID int64 `json:"member_id"`
	Minor    int64 `json:"minor"`
}

type transactionRequest struct {
	Description string         `json:"description"`
	Day         string         `json:"day"`
	Currency    string         `json:"currency"`
	TotalMinor  int64          `json:"total_minor"`
	Payments    []entryRequest `json:"payments"`
	Shares      []entryRequest `json:"shares"`
}

// validate is the whole rule set, in the order a person would hit it. Every
// refusal is a User Error naming the number that is wrong, because "the shares
// do not add up" sends somebody hunting and "12.00 is left to claim" does not.
func validate(req transactionRequest, members []store.Member) (store.Write, error) {
	str := spliffstrings.Default
	out := store.Write{}

	description := strings.TrimSpace(req.Description)
	if description == "" {
		return out, corei18n.User(str.Transaction.Error.DescriptionRequired())
	}
	if len([]rune(description)) > MaxDescriptionRunes {
		return out, corei18n.User(str.Transaction.Error.DescriptionTooLong())
	}
	if _, err := time.Parse(rates.DayFormat, req.Day); err != nil {
		return out, corei18n.User(str.Transaction.Error.DayInvalid())
	}
	currency := money.Normalise(req.Currency)
	if !money.ValidCode(currency) {
		return out, corei18n.User(str.Transaction.Error.CurrencyInvalid())
	}
	if req.TotalMinor <= 0 {
		return out, corei18n.User(str.Transaction.Error.TotalPositive())
	}

	current := map[int64]bool{}
	for _, m := range members {
		current[m.UserID] = true
	}

	payments, paid, err := checkEntries(req.Payments, current, currency, false)
	if err != nil {
		return out, err
	}
	if paid != req.TotalMinor {
		return out, corei18n.User(str.Transaction.Error.PaymentsMismatch(
			money.Format(paid, currency), money.Format(req.TotalMinor, currency)+" "+currency))
	}
	shares, claimed, err := checkEntries(req.Shares, current, currency, true)
	if err != nil {
		return out, err
	}
	if claimed > req.TotalMinor {
		return out, corei18n.User(str.Transaction.Error.SharesOverdraw(
			money.Format(claimed-req.TotalMinor, currency) + " " + currency))
	}

	out = store.Write{
		Description: description, Day: req.Day, Currency: currency,
		TotalMinor: req.TotalMinor, Payments: payments, Shares: shares,
	}
	return out, nil
}

// checkEntries turns one side's rows into entries: one per Member at most, no
// negative amount, and every name a current Member. A zero row is dropped
// rather than refused — a form that lists everybody and leaves some at nothing
// is exactly how an even split across four of six people is typed.
func checkEntries(in []entryRequest, current map[int64]bool, currency string, allowEmpty bool) ([]store.Entry, int64, error) {
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
	_ = currency
	return out, total, nil
}

// snapshot is what History keeps of a Transaction: everything a person can
// change, as JSON, so a diff can be shown without a second schema.
type snapshot struct {
	Description string        `json:"description"`
	Day         string        `json:"day"`
	Currency    string        `json:"currency"`
	TotalMinor  int64         `json:"total_minor"`
	Payments    []store.Entry `json:"payments"`
	Shares      []store.Entry `json:"shares"`
	Photos      int           `json:"photos"`
}

func snapshotOf(t store.Transaction) string {
	s := snapshot{
		Description: t.Description, Day: t.Day, Currency: t.Currency,
		TotalMinor: t.TotalMinor, Payments: t.Payments, Shares: t.Shares,
		Photos: len(t.Photos),
	}
	if s.Payments == nil {
		s.Payments = []store.Entry{}
	}
	if s.Shares == nil {
		s.Shares = []store.Entry{}
	}
	body, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return string(body)
}

func snapshotOfWrite(w store.Write, photos int) string {
	return snapshotOf(store.Transaction{
		Description: w.Description, Day: w.Day, Currency: w.Currency,
		TotalMinor: w.TotalMinor, Payments: w.Payments, Shares: w.Shares,
		Photos: make([]store.Photo, photos),
	})
}

// ---- the writes ----

func (s *server) handleCreateTransaction(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req transactionRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	members, err := store.Members(r.Context(), s.db, sc.GroupID)
	if err != nil {
		return err
	}
	write, err := validate(req, members)
	if err != nil {
		return err
	}
	now := rfc3339(time.Now())
	var id int64
	err = s.withWriteTx(r.Context(), "create-transaction", func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if id, err = store.InsertTransaction(ctx, tx, sc.GroupID, sc.User.UserID, write, now); err != nil {
			return err
		}
		return store.AppendHistory(ctx, tx, id, sc.User.UserID,
			store.HistoryCreated, "", snapshotOfWrite(write, 0), now)
	})
	if err != nil {
		return err
	}
	s.notifyTransaction(transactionNotice{
		GroupID: sc.GroupID, TxID: id, ActorID: sc.User.UserID, Created: true,
	})
	return writeJSON(w, map[string]any{"id": id})
}

func (s *server) handleUpdateTransaction(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req transactionRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	before, err := store.TransactionByID(r.Context(), s.db, sc.TxID)
	if err != nil {
		return notFound(err)
	}
	if before.Deleted() {
		return corei18n.User(spliffstrings.Default.Transaction.Error.Deleted())
	}
	members, err := store.Members(r.Context(), s.db, sc.GroupID)
	if err != nil {
		return err
	}
	write, err := validate(req, members)
	if err != nil {
		return err
	}
	now := rfc3339(time.Now())
	err = s.withWriteTx(r.Context(), "update-transaction", func(ctx context.Context, tx *sql.Tx) error {
		if err := store.UpdateTransaction(ctx, tx, sc.TxID, write, now); err != nil {
			return err
		}
		return store.AppendHistory(ctx, tx, sc.TxID, sc.User.UserID, store.HistoryEdited,
			snapshotOf(before), snapshotOfWrite(write, len(before.Photos)), now)
	})
	if err != nil {
		return err
	}
	s.notifyTransaction(transactionNotice{GroupID: sc.GroupID, TxID: sc.TxID, ActorID: sc.User.UserID})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleDeleteTransaction tombstones: the Transaction leaves the ledger, stays
// in History, and any Member may restore it with its Shares and Photos intact.
func (s *server) handleDeleteTransaction(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	return s.setDeleted(w, r, sc, true)
}

func (s *server) handleRestoreTransaction(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	return s.setDeleted(w, r, sc, false)
}

func (s *server) setDeleted(w http.ResponseWriter, r *http.Request, sc route.Scope, deleted bool) error {
	before, err := store.TransactionByID(r.Context(), s.db, sc.TxID)
	if err != nil {
		return notFound(err)
	}
	if before.Deleted() == deleted {
		w.WriteHeader(http.StatusNoContent) // already there; saying so twice helps nobody
		return nil
	}
	now := rfc3339(time.Now())
	at := now
	kind := store.HistoryDeleted
	if !deleted {
		at, kind = "", store.HistoryRestored
	}
	err = s.withWriteTx(r.Context(), "set-transaction-deleted", func(ctx context.Context, tx *sql.Tx) error {
		if err := store.SetDeleted(ctx, tx, sc.TxID, at); err != nil {
			return err
		}
		return store.AppendHistory(ctx, tx, sc.TxID, sc.User.UserID, kind, snapshotOf(before), "", now)
	})
	if err != nil {
		return err
	}
	s.notifyTransaction(transactionNotice{GroupID: sc.GroupID, TxID: sc.TxID, ActorID: sc.User.UserID})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- the read ----

type transactionViewDTO struct {
	Transaction transactionDTO `json:"transaction"`
	Group       groupHeadDTO   `json:"group"`
	Members     []memberDTO    `json:"members"`
	History     []historyDTO   `json:"history"`
}

type groupHeadDTO struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	BaseCurrency string `json:"base_currency"`
	IsOwner      bool   `json:"is_owner"`
	Me           int64  `json:"me"`
}

func (s *server) handleGetTransaction(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	ctx := r.Context()
	t, err := store.TransactionByID(ctx, s.db, sc.TxID)
	if err != nil {
		return notFound(err)
	}
	g, err := store.GroupByID(ctx, s.db, sc.GroupID)
	if err != nil {
		return notFound(err)
	}
	members, err := store.Members(ctx, s.db, sc.GroupID)
	if err != nil {
		return err
	}
	names := map[int64]string{}
	out := transactionViewDTO{
		Group: groupHeadDTO{
			ID: g.ID, Name: g.Name, BaseCurrency: g.BaseCurrency,
			IsOwner: g.OwnerID == sc.User.UserID, Me: sc.User.UserID,
		},
		Members: []memberDTO{},
	}
	for _, m := range members {
		names[m.UserID] = m.Name
		out.Members = append(out.Members, memberDTO{
			UserID: m.UserID, Name: m.Name, JoinedAt: m.JoinedAt, IsOwner: m.IsOwner,
		})
	}
	book, err := s.rates.Book(ctx)
	if err != nil {
		return err
	}
	if out.Transaction, err = s.transactionDTO(t, g, book, names); err != nil {
		return err
	}
	history, err := store.TransactionHistory(ctx, s.db, sc.TxID)
	if err != nil {
		return err
	}
	out.History = historyDTOs(history, names, map[int64]string{t.ID: t.Description})
	return writeJSON(w, out)
}

// groupOfTransaction is what the dispatcher resolves a {tx} through. It answers
// 0 rather than an error for a Transaction that is not there, so a guessed id
// is a 404 and not a 500.
func (s *server) groupOfTransaction(ctx context.Context, txID int64) (int64, error) {
	var groupID int64
	err := s.db.QueryRowContext(ctx, `select group_id from transactions where id = ?`, txID).Scan(&groupID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return groupID, err
}
