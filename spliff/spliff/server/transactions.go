package spliffserver

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"spliff/spliff/domain/group"
	"spliff/spliff/storage/store"
	"spliff/spliff/web/route"
)

// Every write to a Transaction is the same three steps: decode the request,
// run domain/group's rule inside one write transaction, then send whatever DM
// the rules call for. The rules themselves (who may be named, what History
// keeps) are domain/group's, and they read what they check inside the write.

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

func (req transactionRequest) domain() group.Request {
	return group.Request{
		Description: req.Description, Day: req.Day, Currency: req.Currency,
		TotalMinor: req.TotalMinor,
		Payments:   domainEntries(req.Payments), Shares: domainEntries(req.Shares),
	}
}

func domainEntries(in []entryRequest) []group.Entry {
	out := make([]group.Entry, len(in))
	for i, e := range in {
		out[i] = group.Entry{MemberID: e.MemberID, Minor: e.Minor}
	}
	return out
}

// ---- the writes ----

func (s *server) handleCreateTransaction(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req transactionRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	now := rfc3339(time.Now())
	var id int64
	err := s.withWriteTx(r.Context(), "create-transaction", func(ctx context.Context, tx *sql.Tx) error {
		var err error
		id, err = group.Record(ctx, tx, sc.GroupID, sc.User.UserID, req.domain(), now)
		return err
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
	now := rfc3339(time.Now())
	err := s.withWriteTx(r.Context(), "update-transaction", func(ctx context.Context, tx *sql.Tx) error {
		return group.Edit(ctx, tx, sc.GroupID, sc.TxID, sc.User.UserID, req.domain(), now)
	})
	if err != nil {
		return notFound(err)
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
	now := rfc3339(time.Now())
	var changed bool
	err := s.withWriteTx(r.Context(), "set-transaction-deleted", func(ctx context.Context, tx *sql.Tx) error {
		var err error
		changed, err = group.SetDeleted(ctx, tx, sc.GroupID, sc.TxID, sc.User.UserID, deleted, now)
		return err
	})
	if err != nil {
		return notFound(err)
	}
	// Already in that state: saying so twice helps nobody, and nobody is told.
	if changed {
		s.notifyTransaction(transactionNotice{GroupID: sc.GroupID, TxID: sc.TxID, ActorID: sc.User.UserID})
	}
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
	Me           int64  `json:"me"` // the caller's own member row
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
	names, actors := memberNames(members), userNames(members)
	out := transactionViewDTO{
		Group: groupHeadDTO{
			ID: g.ID, Name: g.Name, BaseCurrency: g.BaseCurrency,
			IsOwner: g.OwnerID == sc.User.UserID, Me: meMember(members, sc.User.UserID),
		},
		Members: memberDTOs(members),
	}
	if err := s.fillTransactionView(ctx, &out, t, g, names, actors); err != nil {
		return err
	}
	return writeJSON(w, out)
}

// fillTransactionView writes the Transaction itself and its History.
func (s *server) fillTransactionView(ctx context.Context, out *transactionViewDTO, t store.Transaction, g store.Group, names, actors map[int64]string) error {
	book, err := s.rates.Book(ctx)
	if err != nil {
		return err
	}
	if out.Transaction, err = s.transactionDTO(t, g, book, names, actors); err != nil {
		return err
	}
	history, err := store.TransactionHistory(ctx, s.db, t.ID)
	if err != nil {
		return err
	}
	out.History = historyDTOs(history, actors, map[int64]string{t.ID: t.Description})
	return nil
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
