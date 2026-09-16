package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"pecheny.me/dopecore/schema"
	"pecheny.me/dopecore/sqlitex"
)

// A fresh database and an upgraded one walk the same list, so a fresh one can
// never tell you whether an upgrade carries its rows across. The phantom step
// rebuilds three tables and re-points every Payment and Share from an account
// to a member row, which is exactly the kind of step a `create table if not
// exists` never has to survive — so it is checked here, against a database that
// was already full.

func openAt(t *testing.T, list []schema.Migration) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spliff.db")
	db, err := sqlitex.Open(path, func(db *sql.DB) error { return schema.Apply(db, list) })
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func exec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func TestTheUpgradeRepointsEveryEntryAtItsMemberRow(t *testing.T) {
	// A v1 database with two people, one bill, one Payment and two Shares, all
	// of them naming ACCOUNTS — which is what v1 stored.
	db := openAt(t, Migrations[:1])
	exec(t, db, `insert into users(id, username, created_at, updated_at) values(7, 'alice', 'now', 'now')`)
	exec(t, db, `insert into users(id, username, created_at, updated_at) values(9, 'bob', 'now', 'now')`)
	exec(t, db, `insert into groups(id, name, base_currency, owner_id, created_at) values(1, 'Tbilisi', 'EUR', 7, 'now')`)
	exec(t, db, `insert into group_members(id, group_id, user_id, joined_at) values(3, 1, 7, 'a')`)
	exec(t, db, `insert into group_members(id, group_id, user_id, joined_at) values(4, 1, 9, 'b')`)
	exec(t, db, `insert into transactions(id, group_id, description, day, currency, total_minor, created_by, created_at, updated_at)
values(1, 1, 'Khinkali', '2026-09-15', 'EUR', 3000, 7, 'now', 'now')`)
	exec(t, db, `insert into transaction_payments(transaction_id, member_id, amount_minor) values(1, 7, 3000)`)
	exec(t, db, `insert into transaction_shares(transaction_id, member_id, amount_minor) values(1, 7, 1000)`)
	exec(t, db, `insert into transaction_shares(transaction_id, member_id, amount_minor) values(1, 9, 2000)`)

	if err := Migrate(db); err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	ctx := context.Background()
	tx, err := TransactionByID(ctx, db, 1)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	// Alice's account is 7 and her member row is 3; the entries now say 3.
	if len(tx.Payments) != 1 || tx.Payments[0].MemberID != 3 || tx.Payments[0].Minor != 3000 {
		t.Errorf("payments = %+v, want alice's member row 3 for 3000", tx.Payments)
	}
	got := map[int64]int64{}
	for _, s := range tx.Shares {
		got[s.MemberID] = s.Minor
	}
	if got[3] != 1000 || got[4] != 2000 || len(got) != 2 {
		t.Errorf("shares = %+v, want member rows 3 and 4 for 1000 and 2000", got)
	}

	// And the member rows themselves survived with their accounts and their
	// join order, which is the Debt graph's tie-break.
	members, err := Members(ctx, db, 1)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("members = %+v, want two", members)
	}
	if members[0].ID != 3 || members[0].UserID != 7 || members[0].Name != "alice" || !members[0].IsOwner {
		t.Errorf("first member = %+v, want alice on row 3, owner", members[0])
	}
	if members[1].ID != 4 || members[1].UserID != 9 || members[1].IsPhantom() {
		t.Errorf("second member = %+v, want bob on row 4 with an account", members[1])
	}
}

// A removed member's row id is never handed to somebody else: a deleted
// Transaction any Member may restore still names whoever it always named.
func TestAMemberRowIdIsNeverReused(t *testing.T) {
	db := openAt(t, Migrations)
	ctx := context.Background()
	exec(t, db, `insert into users(id, username, created_at, updated_at) values(1, 'alice', 'now', 'now')`)
	exec(t, db, `insert into groups(id, name, base_currency, owner_id, created_at) values(1, 'Tbilisi', 'EUR', 1, 'now')`)

	first, err := AddPhantom(ctx, db, 1, "Nino", "a")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := RemoveMember(ctx, db, 1, first); err != nil {
		t.Fatalf("remove: %v", err)
	}
	second, err := AddPhantom(ctx, db, 1, "Gio", "b")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if second == first {
		t.Fatalf("Gio was given Nino's row %d", first)
	}
}

// Claiming is one UPDATE of one row, and the guard on it is what makes two
// people racing for the same Phantom safe.
func TestClaimingAPhantomTakesItOnlyOnce(t *testing.T) {
	db := openAt(t, Migrations)
	ctx := context.Background()
	exec(t, db, `insert into users(id, username, created_at, updated_at) values(1, 'alice', 'now', 'now')`)
	exec(t, db, `insert into users(id, username, created_at, updated_at) values(2, 'bob', 'now', 'now')`)
	exec(t, db, `insert into users(id, username, created_at, updated_at) values(3, 'carol', 'now', 'now')`)
	exec(t, db, `insert into groups(id, name, base_currency, owner_id, created_at) values(1, 'Tbilisi', 'EUR', 1, 'now')`)

	nino, err := AddPhantom(ctx, db, 1, "Nino", "a")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := ClaimPhantom(ctx, db, 1, nino, 2); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := ClaimPhantom(ctx, db, 1, nino, 3); err != ErrNotFound {
		t.Errorf("the second claim = %v, want ErrNotFound", err)
	}
	member, err := MemberByID(ctx, db, 1, nino)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if member.UserID != 2 || member.Name != "bob" || member.IsPhantom() {
		t.Errorf("the row is %+v, want bob's", member)
	}
	// It kept its place in join order, which is what keeps the Debt graph's
	// tie-break the same before and after.
	if member.JoinedAt != "a" {
		t.Errorf("joined_at = %q, want the phantom's own %q", member.JoinedAt, "a")
	}
}

// A Phantom is not in the Group's list of people who can be handed anything,
// and Phantoms() is what the join page offers as "I am …".
func TestPhantomsAreListedApart(t *testing.T) {
	db := openAt(t, Migrations)
	ctx := context.Background()
	exec(t, db, `insert into users(id, username, created_at, updated_at) values(1, 'alice', 'now', 'now')`)
	exec(t, db, `insert into groups(id, name, base_currency, owner_id, created_at) values(1, 'Tbilisi', 'EUR', 1, 'now')`)
	if err := AddMember(ctx, db, 1, 1, "a"); err != nil {
		t.Fatalf("seat the owner: %v", err)
	}
	if _, err := AddPhantom(ctx, db, 1, "Nino", "b"); err != nil {
		t.Fatalf("add: %v", err)
	}
	list, err := Phantoms(ctx, db, 1)
	if err != nil {
		t.Fatalf("phantoms: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Nino" || !list[0].IsPhantom() {
		t.Errorf("phantoms = %+v, want Nino alone", list)
	}
}
