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
		return group.RemoveMember(ctx, tx, oneTable{}, w.groupID, w.bob, now)
	})
	wantRefusal(t, err, str.Group.Error.NotSettled("Bob", "-10.00 EUR"))

	// Bob paid for himself, so he is level, but a live Transaction still names
	// him, and that is enough to keep him.
	err = w.writer.Tx(t.Context(), "test", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := group.Record(ctx, tx, w.groupID, 1, bill(w.bob, w.bob), now); err != nil {
			return err
		}
		return group.RemoveMember(ctx, tx, oneTable{}, w.groupID, w.bob, now)
	})
	wantRefusal(t, err, str.Group.Error.StillNamed("Bob"))
}

func TestRecordingFailsForAMemberRemovedInTheSameTransaction(t *testing.T) {
	w := newWorld(t)
	err := w.writer.Tx(t.Context(), "test", func(ctx context.Context, tx *sql.Tx) error {
		if err := group.RemoveMember(ctx, tx, oneTable{}, w.groupID, w.bob, now); err != nil {
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
		return group.RemoveMember(ctx, tx, oneTable{}, w.groupID, w.alice, now)
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
		return group.RemoveMember(ctx, tx, oneTable{}, w.groupID, w.bob, now)
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
				return group.RemoveMember(ctx, tx, oneTable{}, w.groupID, phantom, now)
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
) e where e.member_id not in (select id from group_members where left_at is null)`).Scan(&ghosts)
	if err != nil {
		t.Fatal(err)
	}
	if ghosts != 0 {
		t.Fatalf("%d entries name somebody who is not in the Group", ghosts)
	}
}

// removeBobAfterADeletedBill records a bill naming Bob, deletes it and removes
// Bob, who is then level and named on nothing live. It answers the bill.
func (w *world) removeBobAfterADeletedBill() int64 {
	var id int64
	w.write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if id, err = group.Record(ctx, tx, w.groupID, 1, bill(w.alice, w.bob), now); err != nil {
			return err
		}
		if _, err := group.SetDeleted(ctx, tx, w.groupID, id, 1, true, now); err != nil {
			return err
		}
		return group.RemoveMember(ctx, tx, oneTable{}, w.groupID, w.bob, now)
	})
	return id
}

// A Former Member is in no list of the Group's people, but the row is still
// there, so the bill that named them can still say who they were.
func TestARemovedMemberKeepsTheirName(t *testing.T) {
	w := newWorld(t)
	w.removeBobAfterADeletedBill()
	ctx := t.Context()

	members, err := store.Members(ctx, w.db, w.groupID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].ID != w.alice {
		t.Errorf("members = %+v, want Alice alone", members)
	}
	if list, err := store.Phantoms(ctx, w.db, w.groupID); err != nil || len(list) != 0 {
		t.Errorf("phantoms = %+v, %v; want none", list, err)
	}
	if _, err := store.MemberByID(ctx, w.db, w.groupID, w.bob); err != store.ErrNotFound {
		t.Errorf("MemberByID(bob) = %v, want ErrNotFound", err)
	}
	all, err := store.AllMembers(ctx, w.db, w.groupID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[1].ID != w.bob || all[1].Name != "Bob" || !all[1].HasLeft() {
		t.Errorf("all members = %+v, want Bob's row kept, marked as left", all)
	}
}

// Every rule that names a current Member treats a Former Member as gone: they
// cannot be named on a bill, removed again, handed the Group, or claimed.
func TestAFormerMemberIsNotAMember(t *testing.T) {
	str := spliffstrings.Default
	w := newWorld(t)
	id := w.removeBobAfterADeletedBill()

	err := w.writer.Tx(t.Context(), "test", func(ctx context.Context, tx *sql.Tx) error {
		_, err := group.Record(ctx, tx, w.groupID, 1, bill(w.alice, w.bob), now)
		return err
	})
	wantRefusal(t, err, str.Transaction.Error.NotAMember())

	err = w.writer.Tx(t.Context(), "test", func(ctx context.Context, tx *sql.Tx) error {
		_, err := group.SetDeleted(ctx, tx, w.groupID, id, 1, false, now)
		return err
	})
	wantRefusal(t, err, str.Transaction.Error.NamesFormerMember())

	err = w.writer.Tx(t.Context(), "test", func(ctx context.Context, tx *sql.Tx) error {
		return group.RemoveMember(ctx, tx, oneTable{}, w.groupID, w.bob, now)
	})
	wantRefusal(t, err, str.Group.Error.NotAMember())

	err = w.writer.Tx(t.Context(), "test", func(ctx context.Context, tx *sql.Tx) error {
		return store.ClaimPhantom(ctx, tx, w.groupID, w.bob, 1)
	})
	if err != store.ErrNotFound {
		t.Errorf("claiming a removed Phantom = %v, want ErrNotFound", err)
	}
}

// Somebody who left and joins again gets their old row back: the same id, so
// a bill deleted while they were away names them again and can be restored.
// Their place in join order is the day they came back.
func TestAFormerMemberWhoRejoinsGetsTheirRowBack(t *testing.T) {
	w := newWorld(t)
	const later = "2026-10-05T12:00:00Z"
	var carolUser, carol, id int64
	w.write(func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
insert into users(username, created_at, updated_at) values('carol', ?, ?)`, now, now)
		if err != nil {
			return err
		}
		if carolUser, err = res.LastInsertId(); err != nil {
			return err
		}
		if err := store.AddMember(ctx, tx, w.groupID, carolUser, now); err != nil {
			return err
		}
		members, err := store.Members(ctx, tx, w.groupID)
		if err != nil {
			return err
		}
		carol = members[len(members)-1].ID
		if id, err = group.Record(ctx, tx, w.groupID, 1, bill(w.alice, carol), now); err != nil {
			return err
		}
		if _, err := group.SetDeleted(ctx, tx, w.groupID, id, 1, true, now); err != nil {
			return err
		}
		return group.Leave(ctx, tx, oneTable{}, w.groupID, carolUser, now)
	})
	if in, err := store.IsMember(t.Context(), w.db, w.groupID, carolUser); err != nil || in {
		t.Fatalf("IsMember after leaving = %v, %v; want false", in, err)
	}
	w.write(func(ctx context.Context, tx *sql.Tx) error {
		if err := store.AddMember(ctx, tx, w.groupID, carolUser, later); err != nil {
			return err
		}
		_, err := group.SetDeleted(ctx, tx, w.groupID, id, 1, false, later)
		return err
	})
	members, err := store.Members(t.Context(), w.db, w.groupID)
	if err != nil {
		t.Fatal(err)
	}
	last := members[len(members)-1]
	if last.ID != carol || last.HasLeft() || last.JoinedAt != later {
		t.Errorf("carol came back as %+v, want row %d, not left, joined %s", last, carol, later)
	}
}
