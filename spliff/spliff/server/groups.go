package spliffserver

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	corei18n "pecheny.me/dopecore/i18nstrings"

	"spliff/spliff/domain/ledger"
	"spliff/spliff/domain/money"
	"spliff/spliff/domain/rates"
	"spliff/spliff/storage/store"
	"spliff/spliff/web/route"

	spliffstrings "spliff/i18nstrings"
)

// MaxNameRunes bounds a Group's name: long enough for "Tbilisi trip, October",
// short enough that the Groups list stays a list.
const MaxNameRunes = 80

// ---- what the browser is handed ----

type memberDTO struct {
	UserID       int64  `json:"user_id"`
	Name         string `json:"name"`
	JoinedAt     string `json:"joined_at"`
	IsOwner      bool   `json:"is_owner"`
	BalanceMinor int64  `json:"balance_minor"`
	Balance      string `json:"balance"`
}

type transferDTO struct {
	FromID   int64  `json:"from_id"`
	FromName string `json:"from_name"`
	ToID     int64  `json:"to_id"`
	ToName   string `json:"to_name"`
	Minor    int64  `json:"minor"`
	Amount   string `json:"amount"`
}

type entryDTO struct {
	MemberID int64  `json:"member_id"`
	Name     string `json:"name"`
	Minor    int64  `json:"minor"`
	Amount   string `json:"amount"`
}

type photoDTO struct {
	ID       int64  `json:"id"`
	URL      string `json:"url"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Uploader string `json:"uploader"`
}

type transactionDTO struct {
	ID             int64      `json:"id"`
	GroupID        int64      `json:"group_id"`
	Description    string     `json:"description"`
	Day            string     `json:"day"`
	Currency       string     `json:"currency"`
	TotalMinor     int64      `json:"total_minor"`
	Total          string     `json:"total"`
	UnclaimedMinor int64      `json:"unclaimed_minor"`
	Unclaimed      string     `json:"unclaimed"`
	Payments       []entryDTO `json:"payments"`
	Shares         []entryDTO `json:"shares"`
	Photos         []photoDTO `json:"photos"`
	RateDate       string     `json:"rate_date"`
	InBase         string     `json:"in_base"`
	Deleted        bool       `json:"deleted"`
	CreatedBy      string     `json:"created_by"`
	UpdatedAt      string     `json:"updated_at"`
}

type historyDTO struct {
	ID            int64  `json:"id"`
	TransactionID int64  `json:"transaction_id"`
	Actor         string `json:"actor"`
	At            string `json:"at"`
	Kind          string `json:"kind"`
	Before        string `json:"before"`
	After         string `json:"after"`
	Description   string `json:"description"`
}

type groupDTO struct {
	ID           int64            `json:"id"`
	Name         string           `json:"name"`
	BaseCurrency string           `json:"base_currency"`
	IsOwner      bool             `json:"is_owner"`
	Me           int64            `json:"me"`
	Members      []memberDTO      `json:"members"`
	Transfers    []transferDTO    `json:"transfers"`
	Live         []transactionDTO `json:"live"`
	Deleted      []transactionDTO `json:"deleted"`
	History      []historyDTO     `json:"history"`
	NoRates      bool             `json:"no_rates"`
}

type groupSummaryDTO struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	BaseCurrency string `json:"base_currency"`
	IsOwner      bool   `json:"is_owner"`
	BalanceMinor int64  `json:"balance_minor"`
	Balance      string `json:"balance"`
}

// ---- the Groups list ----

func (s *server) handleListGroups(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	groups, err := store.GroupsOf(r.Context(), s.db, sc.User.UserID)
	if err != nil {
		return err
	}
	book, err := s.rates.Book(r.Context())
	if err != nil {
		return err
	}
	out := []groupSummaryDTO{}
	for _, g := range groups {
		row := groupSummaryDTO{
			ID: g.ID, Name: g.Name, BaseCurrency: g.BaseCurrency,
			IsOwner: g.OwnerID == sc.User.UserID,
		}
		balances, _, err := s.balancesOf(r.Context(), g, book)
		if err != nil && !errors.Is(err, rates.ErrNoTable) {
			return err
		}
		for _, b := range balances {
			if b.MemberID == sc.User.UserID {
				row.BalanceMinor = b.Minor
				row.Balance = money.Format(b.Minor, g.BaseCurrency)
			}
		}
		out = append(out, row)
	}
	return writeJSON(w, out)
}

type createGroupRequest struct {
	Name         string `json:"name"`
	BaseCurrency string `json:"base_currency"`
}

func (s *server) handleCreateGroup(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req createGroupRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	name, err := checkedName(req.Name)
	if err != nil {
		return err
	}
	currency, err := s.checkedCurrency(r.Context(), req.BaseCurrency)
	if err != nil {
		return err
	}
	now := rfc3339(time.Now())
	var id int64
	err = s.withWriteTx(r.Context(), "create-group", func(ctx context.Context, tx *sql.Tx) error {
		var err error
		id, err = store.CreateGroup(ctx, tx, name, currency, sc.User.UserID, now)
		return err
	})
	if err != nil {
		return err
	}
	return writeJSON(w, map[string]any{"id": id})
}

func checkedName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", corei18n.User(spliffstrings.Default.Group.Error.NameRequired())
	}
	if len([]rune(name)) > MaxNameRunes {
		return "", corei18n.User(spliffstrings.Default.Group.Error.NameTooLong())
	}
	return name, nil
}

// checkedCurrency refuses a code the Rate tables do not carry: a Group stated
// in a currency nothing can convert to would show no balances at all, and the
// person who typed it would have no idea why.
func (s *server) checkedCurrency(ctx context.Context, raw string) (string, error) {
	code := money.Normalise(raw)
	if !money.ValidCode(code) {
		return "", corei18n.User(spliffstrings.Default.Group.Error.CurrencyUnknown())
	}
	book, err := s.rates.Book(ctx)
	if err != nil {
		return "", err
	}
	if book.Empty() {
		// Before the first fetch there is nothing to check against, and
		// refusing every currency would make the app unusable on a fresh
		// instance. The shape check above is what is left.
		return code, nil
	}
	table, err := book.For(time.Now().UTC().Format(rates.DayFormat))
	if err != nil {
		return "", err
	}
	if _, ok := table.Rates[code]; !ok {
		return "", corei18n.User(spliffstrings.Default.Group.Error.CurrencyUnknown())
	}
	return code, nil
}

// handleCurrencies is the base-currency picker's list: the codes the newest
// Rate table actually carries.
func (s *server) handleCurrencies(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	book, err := s.rates.Book(r.Context())
	if err != nil {
		return err
	}
	codes, err := book.Currencies()
	if err != nil {
		return err
	}
	if codes == nil {
		codes = []string{}
	}
	return writeJSON(w, codes)
}

// ---- the Group page ----

// balancesOf is the ledger over a Group's live Transactions: every Member's Net
// balance in the Base currency, in join order.
func (s *server) balancesOf(ctx context.Context, g store.Group, book *Book) ([]ledger.Balance, []store.Member, error) {
	members, err := store.Members(ctx, s.db, g.ID)
	if err != nil {
		return nil, nil, err
	}
	txs, err := store.GroupTransactions(ctx, s.db, g.ID, true)
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
// rate, so the pin is always nil — the argument is there so that the day it is
// stored, this is the one line that changes.
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

func (s *server) handleGetGroup(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	ctx := r.Context()
	g, err := store.GroupByID(ctx, s.db, sc.GroupID)
	if err != nil {
		return notFound(err)
	}
	book, err := s.rates.Book(ctx)
	if err != nil {
		return err
	}
	balances, members, err := s.balancesOf(ctx, g, book)
	if err != nil && !errors.Is(err, rates.ErrNoTable) {
		return err
	}

	out := groupDTO{
		ID: g.ID, Name: g.Name, BaseCurrency: g.BaseCurrency,
		IsOwner: g.OwnerID == sc.User.UserID, Me: sc.User.UserID,
		NoRates:   book.Empty(),
		Members:   []memberDTO{},
		Transfers: []transferDTO{},
	}
	names := map[int64]string{}
	byMember := map[int64]int64{}
	for _, b := range balances {
		byMember[b.MemberID] = b.Minor
	}
	for _, m := range members {
		names[m.UserID] = m.Name
		out.Members = append(out.Members, memberDTO{
			UserID: m.UserID, Name: m.Name, JoinedAt: m.JoinedAt, IsOwner: m.IsOwner,
			BalanceMinor: byMember[m.UserID],
			Balance:      money.Format(byMember[m.UserID], g.BaseCurrency),
		})
	}
	for _, t := range ledger.Transfers(balances) {
		out.Transfers = append(out.Transfers, transferDTO{
			FromID: t.From, FromName: names[t.From], ToID: t.To, ToName: names[t.To],
			Minor: t.Minor, Amount: money.Format(t.Minor, g.BaseCurrency),
		})
	}

	if out.Live, err = s.feed(ctx, g, book, names, true); err != nil {
		return err
	}
	if out.Deleted, err = s.feed(ctx, g, book, names, false); err != nil {
		return err
	}
	history, err := store.GroupHistory(ctx, s.db, g.ID, 200)
	if err != nil {
		return err
	}
	out.History = historyDTOs(history, names, s.descriptions(out.Live, out.Deleted))
	return writeJSON(w, out)
}

func (s *server) descriptions(lists ...[]transactionDTO) map[int64]string {
	out := map[int64]string{}
	for _, list := range lists {
		for _, t := range list {
			out[t.ID] = t.Description
		}
	}
	return out
}

func (s *server) feed(ctx context.Context, g store.Group, book *Book, names map[int64]string, live bool) ([]transactionDTO, error) {
	txs, err := store.GroupTransactions(ctx, s.db, g.ID, live)
	if err != nil {
		return nil, err
	}
	out := make([]transactionDTO, 0, len(txs))
	for _, t := range txs {
		dto, err := s.transactionDTO(t, g, book, names)
		if err != nil {
			return nil, err
		}
		out = append(out, dto)
	}
	return out, nil
}

// transactionDTO is one Transaction as a page reads it: its own amounts in its
// own currency, plus the Rate date it converts with and its total restated in
// the Group's Base currency — "rate as of <date>" is the line this feeds.
func (s *server) transactionDTO(t store.Transaction, g store.Group, book *Book, names map[int64]string) (transactionDTO, error) {
	out := transactionDTO{
		ID: t.ID, GroupID: t.GroupID, Description: t.Description, Day: t.Day,
		Currency: t.Currency, TotalMinor: t.TotalMinor,
		Total:          money.Format(t.TotalMinor, t.Currency),
		UnclaimedMinor: t.Unclaimed(),
		Unclaimed:      money.Format(t.Unclaimed(), t.Currency),
		Deleted:        t.Deleted(), CreatedBy: names[t.CreatedBy], UpdatedAt: t.UpdatedAt,
		Payments: entryDTOs(t.Payments, t.Currency, names),
		Shares:   entryDTOs(t.Shares, t.Currency, names),
		Photos:   photoDTOs(t.Photos, names),
	}
	if book.Empty() {
		return out, nil
	}
	table, err := book.For(t.Day)
	if err != nil {
		return out, err
	}
	out.RateDate = table.Day
	converted, err := rates.Convert(money.Amount{Minor: t.TotalMinor, Currency: t.Currency},
		g.BaseCurrency, table, nil)
	if err != nil {
		// A currency the day's table does not carry is not a reason to hide the
		// Transaction: it is shown in its own currency, with no restatement.
		return out, nil
	}
	out.InBase = money.Format(converted.Minor, g.BaseCurrency)
	return out, nil
}

func entryDTOs(entries []store.Entry, currency string, names map[int64]string) []entryDTO {
	out := make([]entryDTO, 0, len(entries))
	for _, e := range entries {
		out = append(out, entryDTO{
			MemberID: e.MemberID, Name: names[e.MemberID],
			Minor: e.Minor, Amount: money.Format(e.Minor, currency),
		})
	}
	return out
}

func photoDTOs(photos []store.Photo, names map[int64]string) []photoDTO {
	out := make([]photoDTO, 0, len(photos))
	for _, p := range photos {
		out = append(out, photoDTO{
			ID: p.ID, URL: "/api/photos/" + strconv.FormatInt(p.ID, 10),
			Width: p.Width, Height: p.Height, Uploader: names[p.UploaderID],
		})
	}
	return out
}

func historyDTOs(entries []store.HistoryEntry, names, descriptions map[int64]string) []historyDTO {
	out := make([]historyDTO, 0, len(entries))
	for _, h := range entries {
		out = append(out, historyDTO{
			ID: h.ID, TransactionID: h.TransactionID, Actor: names[h.ActorID],
			At: h.At, Kind: h.Kind, Before: h.Before, After: h.After,
			Description: descriptions[h.TransactionID],
		})
	}
	return out
}

// ---- the Group's own settings ----

type patchGroupRequest struct {
	Name         *string `json:"name,omitempty"`
	BaseCurrency *string `json:"base_currency,omitempty"`
}

// handlePatchGroup renames the Group or restates it in another Base currency.
// Every Member may do both: the name and the currency belong to the Group, not
// to the Owner.
func (s *server) handlePatchGroup(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req patchGroupRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	var name, currency string
	var err error
	if req.Name != nil {
		if name, err = checkedName(*req.Name); err != nil {
			return err
		}
	}
	if req.BaseCurrency != nil {
		if currency, err = s.checkedCurrency(r.Context(), *req.BaseCurrency); err != nil {
			return err
		}
	}
	err = s.withWriteTx(r.Context(), "patch-group", func(ctx context.Context, tx *sql.Tx) error {
		if name != "" {
			if err := store.SetGroupName(ctx, tx, sc.GroupID, name); err != nil {
				return err
			}
		}
		if currency != "" {
			return store.SetBaseCurrency(ctx, tx, sc.GroupID, currency)
		}
		return nil
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handOverRequest names the Member the Group is being handed to.
type handOverRequest struct {
	UserID int64 `json:"user_id"`
}

func (s *server) handleHandOver(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req handOverRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	member, err := store.IsMember(r.Context(), s.db, sc.GroupID, req.UserID)
	if err != nil {
		return err
	}
	if !member {
		return corei18n.User(spliffstrings.Default.Group.Error.NotAMember())
	}
	err = s.withWriteTx(r.Context(), "hand-over-group", func(ctx context.Context, tx *sql.Tx) error {
		return store.SetOwner(ctx, tx, sc.GroupID, req.UserID)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleDeleteGroup destroys a Group once nobody is owed anything. The refusal
// names the amount, because "you cannot delete this" without a number is a
// message that sends somebody hunting.
func (s *server) handleDeleteGroup(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	ctx := r.Context()
	g, err := store.GroupByID(ctx, s.db, sc.GroupID)
	if err != nil {
		return notFound(err)
	}
	book, err := s.rates.Book(ctx)
	if err != nil {
		return err
	}
	balances, members, err := s.balancesOf(ctx, g, book)
	if err != nil && !errors.Is(err, rates.ErrNoTable) {
		return err
	}
	names := map[int64]string{}
	for _, m := range members {
		names[m.UserID] = m.Name
	}
	for _, b := range balances {
		if b.Minor != 0 {
			return corei18n.User(spliffstrings.Default.Group.Error.NotSettled(
				names[b.MemberID], money.Format(b.Minor, g.BaseCurrency)+" "+g.BaseCurrency))
		}
	}
	refs, err := store.GroupPhotoRefs(ctx, s.db, g.ID)
	if err != nil {
		return err
	}
	if err := s.withWriteTx(ctx, "delete-group", func(ctx context.Context, tx *sql.Tx) error {
		return store.DeleteGroup(ctx, tx, g.ID)
	}); err != nil {
		return err
	}
	// The blobs go after the commit: one removed inside the transaction would be
	// gone even if the transaction rolled back.
	for _, ref := range refs {
		logDropped("delete-group: blob", s.blobs.Remove(ref))
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- leaving and being kicked ----

func (s *server) handleLeaveGroup(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	return s.removeMember(w, r, sc, sc.User.UserID, false)
}

func (s *server) handleKickMember(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	id, err := strconv.ParseInt(r.PathValue("userId"), 10, 64)
	if err != nil {
		return route.NotFound(spliffstrings.Default.Group.Error.NotAMember())
	}
	return s.removeMember(w, r, sc, id, true)
}

// removeMember is the one rule both ways round: nobody goes while their Net
// balance is non-zero, and the Owner cannot go at all without handing the Group
// on first. A Member who is level but still named on a live Transaction stays
// too — the Debt graph must never name somebody who is not there.
func (s *server) removeMember(w http.ResponseWriter, r *http.Request, sc route.Scope, userID int64, kick bool) error {
	ctx := r.Context()
	str := spliffstrings.Default
	g, err := store.GroupByID(ctx, s.db, sc.GroupID)
	if err != nil {
		return notFound(err)
	}
	if g.OwnerID == userID {
		return corei18n.User(str.Group.Error.OwnerMustHandOver())
	}
	if kick && !sc.IsOwner {
		return route.Forbidden(str.Group.Error.OwnerOnly())
	}
	member, err := store.IsMember(ctx, s.db, sc.GroupID, userID)
	if err != nil {
		return err
	}
	if !member {
		return corei18n.User(str.Group.Error.NotAMember())
	}
	book, err := s.rates.Book(ctx)
	if err != nil {
		return err
	}
	balances, members, err := s.balancesOf(ctx, g, book)
	if err != nil && !errors.Is(err, rates.ErrNoTable) {
		return err
	}
	name := ""
	for _, m := range members {
		if m.UserID == userID {
			name = m.Name
		}
	}
	for _, b := range balances {
		if b.MemberID == userID && b.Minor != 0 {
			return corei18n.User(str.Group.Error.NotSettled(
				name, money.Format(b.Minor, g.BaseCurrency)+" "+g.BaseCurrency))
		}
	}
	named, err := store.MemberHasEntries(ctx, s.db, sc.GroupID, userID)
	if err != nil {
		return err
	}
	if named {
		return corei18n.User(str.Group.Error.StillNamed(name))
	}
	if err := s.withWriteTx(ctx, "remove-member", func(ctx context.Context, tx *sql.Tx) error {
		return store.RemoveMember(ctx, tx, sc.GroupID, userID)
	}); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// notFound turns the store's missing-row answer into a 404 in our words.
func notFound(err error) error {
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
		return route.NotFound(spliffstrings.Default.Server.Error.NotFound())
	}
	return err
}
