package group_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopecore/sqlitex"
	"pecheny.me/dopecore/sqlitex/sqlitextest"

	"spliff/spliff/domain/group"
	"spliff/spliff/domain/rates"
	"spliff/spliff/storage/store"

	spliffstrings "spliff/i18nstrings"
)

// These tests drive the rules with no HTTP in between: a real database, the
// Writer the server uses, and the functions the handlers call inside it.

const (
	now = "2026-10-01T12:00:00Z"
	day = "2026-10-01"
	ten = 1000 // 10.00 EUR in minor units
)

// oneTable is a Rate book with a single day in it. Every bill here is in EUR,
// the Base currency, so the rates never move a balance.
type oneTable struct{}

func (oneTable) For(string) (rates.Table, error) {
	return rates.Table{Day: day, Rates: map[string]string{"USD": "1", "EUR": "0.5"}}, nil
}
func (oneTable) Empty() bool { return false }

type world struct {
	t       *testing.T
	db      *sql.DB
	writer  sqlitex.Writer
	groupID int64
	alice   int64 // the Owner's member row
	bob     int64 // a Phantom's member row
}

func newWorld(t *testing.T) *world {
	t.Helper()
	db, err := sqlitextest.Open(filepath.Join(t.TempDir(), "spliff.db"), store.Migrate)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	w := &world{t: t, db: db, writer: sqlitex.Writer{DB: db, Mu: &sync.Mutex{}}}
	w.write(func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
insert into users(username, created_at, updated_at) values('alice', ?, ?)`, now, now)
		if err != nil {
			return err
		}
		owner, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if w.groupID, err = group.Create(ctx, tx, "Tbilisi", "EUR", owner, now); err != nil {
			return err
		}
		members, err := store.Members(ctx, tx, w.groupID)
		if err != nil {
			return err
		}
		w.alice = members[0].ID
		w.bob, err = group.AddPhantom(ctx, tx, w.groupID, "Bob", now)
		return err
	})
	return w
}

func (w *world) write(fn func(ctx context.Context, tx *sql.Tx) error) {
	w.t.Helper()
	if err := w.writer.Tx(w.t.Context(), "test", fn); err != nil {
		w.t.Fatal(err)
	}
}

// bill is 10.00 EUR that payer handed over and holder answers for.
func bill(payer, holder int64) group.Request {
	return group.Request{
		Description: "Dinner", Day: day, Currency: "EUR", TotalMinor: ten,
		Payments: []group.Entry{{MemberID: payer, Minor: ten}},
		Shares:   []group.Entry{{MemberID: holder, Minor: ten}},
	}
}

func wantRefusal(t *testing.T, err error, want string) {
	t.Helper()
	got, ok := corei18n.AsUser(err)
	if !ok {
		t.Fatalf("got %v, want the refusal %q", err, want)
	}
	if got != want {
		t.Fatalf("got the refusal %q, want %q", got, want)
	}
}

func TestRemovingAMemberFailsWhileATransactionNamesThem(t *testing.T) {
	str := spliffstrings.Default
	w := newWorld(t)
	err := w.writer.Tx(t.Context(), "test", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := group.Record(ctx, tx, w.groupID, 1, bill(w.alice, w.bob), now); err != nil {
			return err
		}
		return group.RemoveMember(ctx, tx, oneTable{}, w.groupID, w.bob)
	})
	wantRefusal(t, err, str.Group.Error.NotSettled("Bob", "-10.00 EUR"))

	// Bob paid for himself, so he is level, but a live Transaction still names
	// him, and that is enough to keep him.
	err = w.writer.Tx(t.Context(), "test", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := group.Record(ctx, tx, w.groupID, 1, bill(w.bob, w.bob), now); err != nil {
			return err
		}
		return group.RemoveMember(ctx, tx, oneTable{}, w.groupID, w.bob)
	})
	wantRefusal(t, err, str.Group.Error.StillNamed("Bob"))
}

func TestRecordingFailsForAMemberRemovedInTheSameTransaction(t *testing.T) {
	w := newWorld(t)
	err := w.writer.Tx(t.Context(), "test", func(ctx context.Context, tx *sql.Tx) error {
		if err := group.RemoveMember(ctx, tx, oneTable{}, w.groupID, w.bob); err != nil {
			return err
		}
		_, err := group.Record(ctx, tx, w.groupID, 1, bill(w.alice, w.bob), now)
		return err
	})
	wantRefusal(t, err, spliffstrings.Default.Transaction.Error.NotAMember())
}

func TestTheOwnerCannotBeRemoved(t *testing.T) {
	w := newWorld(t)
	err := w.writer.Tx(t.Context(), "test", func(ctx context.Context, tx *sql.Tx) error {
		return group.RemoveMember(ctx, tx, oneTable{}, w.groupID, w.alice)
	})
	wantRefusal(t, err, spliffstrings.Default.Group.Error.OwnerMustHandOver())
}

// The "before" an edit records is the Transaction as the write transaction
// sees it. The raw UPDATE stands in for a write that committed between the
// page loading and the edit arriving.
func TestHistoryBeforeIsReadInsideTheWrite(t *testing.T) {
	w := newWorld(t)
	var id int64
	w.write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		id, err = group.Record(ctx, tx, w.groupID, 1, bill(w.alice, w.alice), now)
		return err
	})
	w.write(func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`update transactions set description = 'Lunch' where id = ?`, id); err != nil {
			return err
		}
		req := bill(w.alice, w.alice)
		req.Description = "Supper"
		return group.Edit(ctx, tx, w.groupID, id, 1, req, now)
	})
	history, err := store.TransactionHistory(t.Context(), w.db, id)
	if err != nil {
		t.Fatal(err)
	}
	last := history[len(history)-1]
	if !strings.Contains(last.Before, `"Lunch"`) || !strings.Contains(last.After, `"Supper"`) {
		t.Fatalf("edit recorded before=%s after=%s", last.Before, last.After)
	}
}

// A deleted Transaction holds nobody in the Group, so somebody it names may
// leave. Restoring it afterwards would put a former Member back in the ledger.
func TestRestoreRefusesATransactionNamingAFormerMember(t *testing.T) {
	w := newWorld(t)
	var id int64
	w.write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if id, err = group.Record(ctx, tx, w.groupID, 1, bill(w.alice, w.bob), now); err != nil {
			return err
		}
		if _, err := group.SetDeleted(ctx, tx, w.groupID, id, 1, true, now); err != nil {
			return err
		}
		return group.RemoveMember(ctx, tx, oneTable{}, w.groupID, w.bob)
	})
	err := w.writer.Tx(t.Context(), "test", func(ctx context.Context, tx *sql.Tx) error {
		_, err := group.SetDeleted(ctx, tx, w.groupID, id, 1, false, now)
		return err
	})
	wantRefusal(t, err, spliffstrings.Default.Transaction.Error.NamesFormerMember())
}

func TestDeletingTwiceChangesNothing(t *testing.T) {
	w := newWorld(t)
	w.write(func(ctx context.Context, tx *sql.Tx) error {
		id, err := group.Record(ctx, tx, w.groupID, 1, bill(w.alice, w.alice), now)
		if err != nil {
			return err
		}
		for i, want := range []bool{true, false} {
			changed, err := group.SetDeleted(ctx, tx, w.groupID, id, 1, true, now)
			if err != nil {
				return err
			}
			if changed != want {
				t.Errorf("delete %d: changed = %v, want %v", i+1, changed, want)
			}
		}
		return nil
	})
}

// A remove and a record that race through the Writer: whichever goes second
// sees the first one's result, so exactly one of them is refused and no
// Transaction is ever left naming somebody who is not in the Group.
func TestARemoveAndARecordThatRaceLeaveNoGhost(t *testing.T) {
	const rounds = 20
	w := newWorld(t)
	for range rounds {
		var phantom int64
		w.write(func(ctx context.Context, tx *sql.Tx) error {
			var err error
			phantom, err = group.AddPhantom(ctx, tx, w.groupID, "Carol", now)
			return err
		})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		wg.Go(func() {
			errs[0] = w.writer.Tx(t.Context(), "remove", func(ctx context.Context, tx *sql.Tx) error {
				return group.RemoveMember(ctx, tx, oneTable{}, w.groupID, phantom)
			})
		})
		wg.Go(func() {
			errs[1] = w.writer.Tx(t.Context(), "record", func(ctx context.Context, tx *sql.Tx) error {
				_, err := group.Record(ctx, tx, w.groupID, 1, bill(phantom, phantom), now)
				return err
			})
		})
		wg.Wait()
		if (errs[0] == nil) == (errs[1] == nil) {
			t.Fatalf("remove: %v, record: %v; want exactly one refused", errs[0], errs[1])
		}
	}
	var ghosts int
	err := w.db.QueryRow(`
select count(*) from (
  select member_id from transaction_payments union all select member_id from transaction_shares
) e where e.member_id not in (select id from group_members)`).Scan(&ghosts)
	if err != nil {
		t.Fatal(err)
	}
	if ghosts != 0 {
		t.Fatalf("%d entries name somebody who is not in the Group", ghosts)
	}
}
