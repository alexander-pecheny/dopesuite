package invitelink

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// memScope is an app's membership in a map: enough to drive the state machine
// without a users or a members table, the way tglogin's memUsers is.
type memScope struct {
	members map[int64]map[int64]bool // scope id → user ids
	names   map[int64]string
	titles  map[int64]string
	nudges  [][2]int64
	addErr  error
}

func newScope() *memScope {
	return &memScope{
		members: map[int64]map[int64]bool{},
		names:   map[int64]string{1: "ann", 2: "bob", 3: "cid"},
		titles:  map[int64]string{1: "the trip"},
	}
}

func (m *memScope) IsMember(_ context.Context, _ Querier, scopeID, userID int64) (bool, error) {
	return m.members[scopeID][userID], nil
}

func (m *memScope) AddMember(_ context.Context, _ Tx, scopeID, userID int64) error {
	if m.addErr != nil {
		return m.addErr
	}
	if m.members[scopeID] == nil {
		m.members[scopeID] = map[int64]bool{}
	}
	m.members[scopeID][userID] = true
	return nil
}

func (m *memScope) remove(scopeID, userID int64) { delete(m.members[scopeID], userID) }

func (m *memScope) DisplayName(_ context.Context, _ Querier, scopeID int64) (string, error) {
	return m.titles[scopeID], nil
}

func (m *memScope) Names(_ context.Context, _ Querier, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	for _, id := range ids {
		out[id] = m.names[id]
	}
	return out, nil
}

func (m *memScope) NudgeOwner(scopeID, requesterID int64) {
	m.nudges = append(m.nudges, [2]int64{scopeID, requesterID})
}

// The two tables the package owns, under names no app uses, so nothing here
// leans on xy's spelling. The columns are the ones xy's v23 migration wrote.
const schema = `
create table scopes(id integer primary key, deleted_at text);
create table links(
  id integer primary key,
  group_id integer not null references scopes(id) on delete cascade,
  code text not null unique,
  label text,
  created_by integer not null,
  created_at text not null,
  expires_at text,
  max_uses integer,
  requires_approval integer not null default 0,
  revoked_at text
);
create table link_uses(
  id integer primary key,
  invite_id integer not null references links(id) on delete cascade,
  user_id integer not null,
  status text not null check (status in ('joined','pending','declined')),
  requested_at text not null,
  decided_at text,
  unique(invite_id, user_id)
);
insert into scopes(id) values(1), (2);
`

func newFixture(t *testing.T) (*sql.DB, Links, *memScope) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	scope := newScope()
	return db, Links{
		Invites:   "links",
		Uses:      "link_uses",
		ScopeID:   "group_id",
		ScopeJoin: "join scopes s on s.id = i.group_id and s.deleted_at is null",
		Scope:     scope,
	}, scope
}

// write runs fn in a transaction, the way an app's write discipline does.
func write(t *testing.T, db *sql.DB, fn func(tx *sql.Tx) error) error {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func mint(t *testing.T, db *sql.DB, l Links, code string, opt Options) Link {
	t.Helper()
	var id int64
	if err := write(t, db, func(tx *sql.Tx) error {
		var err error
		id, err = l.Mint(context.Background(), tx, 1, 1, code, opt, time.Now())
		return err
	}); err != nil {
		t.Fatalf("mint: %v", err)
	}
	lk, err := l.ByID(context.Background(), db, id)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	return lk
}

func join(t *testing.T, db *sql.DB, l Links, code string, userID int64, now time.Time) (Result, error) {
	t.Helper()
	var res Result
	err := write(t, db, func(tx *sql.Tx) error {
		var err error
		res, err = l.Join(context.Background(), tx, code, userID, now)
		return err
	})
	if err == nil && res.Nudge {
		l.Nudge(res.ScopeID, userID)
	}
	return res, err
}

func decide(t *testing.T, db *sql.DB, l Links, userID int64, approve bool) error {
	t.Helper()
	return write(t, db, func(tx *sql.Tx) error {
		return l.Decide(context.Background(), tx, 1, userID, approve, time.Now())
	})
}

// TestJoinAdmitsAndLists: a plain link admits a stranger, and the owner's list
// then names who came in through it.
func TestJoinAdmitsAndLists(t *testing.T) {
	db, l, scope := newFixture(t)
	lk := mint(t, db, l, "PLAIN", Options{Label: "  for testers  "})
	if lk.Label != "for testers" {
		t.Fatalf("label = %q, want it trimmed", lk.Label)
	}
	if lk.MaxUses != nil || lk.Left() != nil || lk.State(time.Now()) != Active {
		t.Fatalf("fresh uncapped link = %+v, want an active link with no cap", lk)
	}

	res, err := join(t, db, l, "PLAIN", 2, time.Now())
	if err != nil || res.State != Member || res.Nudge {
		t.Fatalf("join = %+v, %v; want a member and no nudge", res, err)
	}
	if !scope.members[1][2] {
		t.Fatal("the joiner is not in the scope")
	}

	links, err := l.List(context.Background(), db, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Used != 1 {
		t.Fatalf("list = %+v, want one link with one use", links)
	}
	if len(links[0].Joined) != 1 || links[0].Joined[0].Name != "bob" || links[0].Joined[0].UserID != 2 {
		t.Fatalf("joined = %+v, want bob", links[0].Joined)
	}
	if len(links[0].Waiting) != 0 {
		t.Fatalf("waiting = %+v, want nobody", links[0].Waiting)
	}
}

// TestSeatsExpiryAndRevoke: the three ways a link stops working, and the fact
// that a member following it again spends nothing.
func TestSeatsExpiryAndRevoke(t *testing.T) {
	db, l, _ := newFixture(t)
	now := time.Now()

	one := mint(t, db, l, "ONESEAT", Options{MaxUses: 1})
	if _, err := join(t, db, l, "ONESEAT", 2, now); err != nil {
		t.Fatal(err)
	}
	if _, err := join(t, db, l, "ONESEAT", 3, now); !refusedWith(err, Exhausted) {
		t.Fatalf("second joiner got %v, want a refusal naming exhausted", err)
	}
	// The member follows it again: already in, and no second use spent.
	res, err := join(t, db, l, "ONESEAT", 2, now)
	if err != nil || res.State != Member {
		t.Fatalf("re-follow = %+v, %v; want member", res, err)
	}
	again, err := l.ByID(context.Background(), db, one.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Used != 1 || *again.Left() != 0 || again.State(now) != Exhausted {
		t.Fatalf("link = %+v, want one use, no seats and exhausted", again)
	}

	// An expiry that has passed refuses everyone. Backdating the row is the only
	// way to reach it without waiting an hour.
	timed := mint(t, db, l, "TIMED", Options{TTLHours: 1})
	if _, err := db.Exec(`update links set expires_at = ? where id = ?`,
		stamp(now.Add(-time.Minute)), timed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := join(t, db, l, "TIMED", 3, now); !refusedWith(err, Expired) {
		t.Fatalf("lapsed link gave %v, want expired", err)
	}

	// A link with seats left still dies when revoked, and keeps its history.
	open := mint(t, db, l, "OPEN", Options{})
	if err := write(t, db, func(tx *sql.Tx) error { return l.Revoke(context.Background(), tx, open.ID, now) }); err != nil {
		t.Fatal(err)
	}
	if _, err := join(t, db, l, "OPEN", 3, now); !refusedWith(err, Revoked) {
		t.Fatalf("revoked link gave %v, want revoked", err)
	}
	links, err := l.List(context.Background(), db, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 3 {
		t.Fatalf("list = %d links, want the three minted", len(links))
	}
	if links[2].ID != one.ID || len(links[2].Joined) != 1 {
		t.Fatalf("the oldest link lost its history: %+v", links[2])
	}
}

// TestApprovalQueue: a link that needs approval queues the joiner instead of
// admitting them, the queue may grow past the cap because only an approval
// spends a seat, and a decline is final for that link.
func TestApprovalQueue(t *testing.T) {
	db, l, scope := newFixture(t)
	now := time.Now()
	mint(t, db, l, "ASK", Options{MaxUses: 1, RequiresApproval: true})

	res, err := join(t, db, l, "ASK", 2, now)
	if err != nil || res.State != Pending || !res.Nudge {
		t.Fatalf("join = %+v, %v; want a pending request that nudges", res, err)
	}
	if scope.members[1][2] {
		t.Fatal("a waiting request made them a member")
	}
	if len(scope.nudges) != 1 || scope.nudges[0] != [2]int64{1, 2} {
		t.Fatalf("nudges = %v, want one for the requester", scope.nudges)
	}
	if res, err := join(t, db, l, "ASK", 3, now); err != nil || res.State != Pending {
		t.Fatalf("second join = %+v, %v; want the queue past the cap", res, err)
	}

	links, err := l.List(context.Background(), db, 1)
	if err != nil {
		t.Fatal(err)
	}
	if links[0].Used != 0 || *links[0].Left() != 1 || len(links[0].Waiting) != 2 {
		t.Fatalf("link with two waiting = %+v, want 0 used, 1 left, 2 waiting", links[0])
	}
	if links[0].Waiting[0].Name != "bob" || links[0].Waiting[1].Name != "cid" {
		t.Fatalf("waiting = %+v, want them in the order they asked", links[0].Waiting)
	}

	if err := decide(t, db, l, 2, true); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !scope.members[1][2] {
		t.Fatal("approving did not admit")
	}
	// The seat is gone, so the second cannot be approved into it.
	if err := decide(t, db, l, 3, true); !errors.Is(err, ErrNoSeatsLeft) {
		t.Fatalf("approve past the cap = %v, want ErrNoSeatsLeft", err)
	}
	if err := decide(t, db, l, 3, false); err != nil {
		t.Fatalf("decline: %v", err)
	}
	peek, err := l.PeekCode(context.Background(), db, "ASK", 3, now)
	if err != nil {
		t.Fatal(err)
	}
	if peek.State != Declined || peek.ScopeName != "the trip" {
		t.Fatalf("peek = %+v, want a declined state and the scope's name", peek)
	}
	// Asking again through the same link is refused, not re-queued.
	if _, err := join(t, db, l, "ASK", 3, now); !refusedWith(err, Declined) {
		t.Fatalf("re-ask gave %v, want declined", err)
	}
	if err := decide(t, db, l, 3, true); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("deciding a settled request = %v, want ErrRequestNotFound", err)
	}
}

// TestOnePersonOneRequest: waiting is scope-wide. Two links must not let one
// person queue twice — approving both would spend two seats, and a decide would
// pick between their rows arbitrarily.
func TestOnePersonOneRequest(t *testing.T) {
	db, l, _ := newFixture(t)
	now := time.Now()
	mint(t, db, l, "FIRST", Options{RequiresApproval: true})
	mint(t, db, l, "SECOND", Options{RequiresApproval: true})

	if _, err := join(t, db, l, "FIRST", 2, now); err != nil {
		t.Fatal(err)
	}
	peek, err := l.PeekCode(context.Background(), db, "SECOND", 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if peek.State != Pending {
		t.Fatalf("second link peek = %q, want pending — the request belongs to the scope", peek.State)
	}
	if res, _ := join(t, db, l, "SECOND", 2, now); res.State != Pending {
		t.Fatalf("second join = %q, want the existing request", res.State)
	}

	links, err := l.List(context.Background(), db, 1)
	if err != nil {
		t.Fatal(err)
	}
	waiting := 0
	for _, lk := range links {
		waiting += len(lk.Waiting)
	}
	if waiting != 1 {
		t.Fatalf("owner sees %d requests, want one person once", waiting)
	}
	if err := decide(t, db, l, 2, true); err != nil {
		t.Fatal(err)
	}
	links, err = l.List(context.Background(), db, 1)
	if err != nil {
		t.Fatal(err)
	}
	used := 0
	for _, lk := range links {
		used += int(lk.Used)
	}
	if used != 1 {
		t.Fatalf("approving spent %d seats, want one", used)
	}
}

// TestRemovedMemberStaysOut: a link that already admitted someone the owner has
// since removed does not quietly let them back in.
func TestRemovedMemberStaysOut(t *testing.T) {
	db, l, scope := newFixture(t)
	now := time.Now()
	mint(t, db, l, "PLAIN", Options{})
	if _, err := join(t, db, l, "PLAIN", 2, now); err != nil {
		t.Fatal(err)
	}
	scope.remove(1, 2)

	peek, err := l.PeekCode(context.Background(), db, "PLAIN", 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if peek.State != Spent {
		t.Fatalf("peek after removal = %q, want spent", peek.State)
	}
	if _, err := join(t, db, l, "PLAIN", 2, now); !refusedWith(err, Spent) {
		t.Fatalf("re-join gave %v, want spent", err)
	}
}

// TestDeleteTakesTheHistory: deleting a link takes its uses with it and leaves
// the member it admitted in place.
func TestDeleteTakesTheHistory(t *testing.T) {
	db, l, scope := newFixture(t)
	lk := mint(t, db, l, "PLAIN", Options{})
	if _, err := join(t, db, l, "PLAIN", 2, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := write(t, db, func(tx *sql.Tx) error { return l.Delete(context.Background(), tx, lk.ID) }); err != nil {
		t.Fatal(err)
	}
	links, err := l.List(context.Background(), db, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Fatalf("links after delete = %+v, want none", links)
	}
	if !scope.members[1][2] {
		t.Fatal("deleting the link evicted the member it had admitted")
	}
	var uses int
	if err := db.QueryRow(`select count(*) from link_uses`).Scan(&uses); err != nil {
		t.Fatal(err)
	}
	if uses != 0 {
		t.Fatalf("%d uses survived the delete", uses)
	}
	if _, err := l.ByCode(context.Background(), db, "PLAIN"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("code after delete = %v, want ErrNotFound", err)
	}
}

// TestScopeGoneKillsTheLink: a link resolves off its own table alone, so the
// adapter's ScopeJoin is the only thing that sees a scope go away.
func TestScopeGoneKillsTheLink(t *testing.T) {
	db, l, _ := newFixture(t)
	mint(t, db, l, "PLAIN", Options{})
	if _, err := db.Exec(`update scopes set deleted_at = ? where id = 1`, stamp(time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := l.PeekCode(context.Background(), db, "PLAIN", 2, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("peek on a dead scope = %v, want ErrNotFound", err)
	}
	if _, err := join(t, db, l, "PLAIN", 2, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("join on a dead scope = %v, want ErrNotFound", err)
	}
}

// TestMintLimits: the caps a link's own settings must respect, refused before
// anything is written.
func TestMintLimits(t *testing.T) {
	db, l, _ := newFixture(t)
	for _, tc := range []struct {
		name string
		opt  Options
		want error
	}{
		{"an hour past a year", Options{TTLHours: MaxTTLHours + 1}, ErrLimitsOutOfRange},
		{"a negative cap", Options{MaxUses: -1}, ErrLimitsOutOfRange},
		{"a negative ttl", Options{TTLHours: -1}, ErrLimitsOutOfRange},
		{"a label past the cap", Options{Label: strings.Repeat("ä", MaxLabelRunes+1)}, ErrLabelTooLong},
		{"a label at the cap", Options{Label: strings.Repeat("ä", MaxLabelRunes)}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.opt.Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("Validate = %v, want %v", err, tc.want)
			}
			err := write(t, db, func(tx *sql.Tx) error {
				_, err := l.Mint(context.Background(), tx, 1, 1, tc.name, tc.opt, time.Now())
				return err
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Mint = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestExpiryUnparsableFailsClosed: a stamp nothing can read must fail the link
// shut, not leave it open forever.
func TestExpiryUnparsableFailsClosed(t *testing.T) {
	db, l, _ := newFixture(t)
	lk := mint(t, db, l, "TIMED", Options{TTLHours: 24})
	if _, err := db.Exec(`update links set expires_at = 'whenever' where id = ?`, lk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := join(t, db, l, "TIMED", 2, time.Now()); !refusedWith(err, Expired) {
		t.Fatalf("unreadable expiry gave %v, want expired", err)
	}
}

func refusedWith(err error, state State) bool {
	var refused *Refused
	return errors.As(err, &refused) && refused.State == state
}
