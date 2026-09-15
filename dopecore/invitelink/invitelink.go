// Package invitelink is the server side of an Invite Link: a URL the owner of
// a scope mints that admits its holder to that scope as a member. A link may
// cap uses, expire, or hold the joiner for approval as a Join Request, and it
// is revocable. It grants MEMBERSHIP and nothing else — never a role, never a
// key: xy's joiner still lands on the passphrase overlay.
//
// The state machine and the SQL on the two tables live here; the app brings its
// write transaction, the table names, what a scope is (Scope) and every word a
// person reads. That is the shape dopecore/tglogin settled on (root
// docs/adr/0004): the package answers a State or a sentinel error, and the app
// maps each to its own HTTP status and wording.
//
// Only a use that reached 'joined' spends the cap, so a queue of hopefuls
// behind a one-seat link costs nothing and a decline refunds nothing. A
// declined row stays, and its unique(invite_id, user_id) is what stops the
// declined asking again.
package invitelink

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Querier is any read handle — the pool or a transaction.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Tx is the write transaction the app opens and bounds.
type Tx interface {
	Querier
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// State is why a link does or does not work. The first four are the link's own
// condition, in the order the reasons override each other: a revoked link is
// revoked whether or not it also expired. The rest belong to one caller — their
// history with this scope overrides the link's own state.
type State string

const (
	Active    State = "active"
	Revoked   State = "revoked"
	Expired   State = "expired"
	Exhausted State = "exhausted"

	Member   State = "member"   // already in, nothing to do
	Pending  State = "pending"  // a Join Request of theirs is waiting
	Declined State = "declined" // this link said no to them, for good
	Spent    State = "spent"    // it admitted them once and they are gone since
)

// The refusals. Each is the app's to word: an app maps ErrNotFound to a 404 in
// its own language, the rest to whatever status its edge uses for a request a
// person can fix.
var (
	ErrNotFound         = errors.New("invite link not found")
	ErrRequestNotFound  = errors.New("no join request waiting")
	ErrNoSeatsLeft      = errors.New("no seats left on the link")
	ErrLimitsOutOfRange = errors.New("invite limits out of range")
	ErrLabelTooLong     = errors.New("invite label too long")
)

// Refused is Join's answer when the link will not admit this caller; State says
// why, and the app words it.
type Refused struct{ State State }

func (e *Refused) Error() string { return "invite link refused: " + string(e.State) }

// A year is longer than any link should live, and short enough that the hours
// cannot overflow the Duration they become. The label cap mirrors the field's
// own maxlength, which is a hint and not a check.
const (
	MaxTTLHours   = 24 * 366
	MaxLabelRunes = 100
)

// Scope is the thing a link admits people to — an xy board, a Spliff Group.
// The package never writes SQL against it, because no two apps keep membership
// the same way.
type Scope interface {
	// IsMember reports whether this person is already in the scope.
	IsMember(ctx context.Context, q Querier, scopeID, userID int64) (bool, error)
	// AddMember puts them in, idempotently: a race on the last seat must not
	// fail the second writer with a unique violation.
	AddMember(ctx context.Context, tx Tx, scopeID, userID int64) error
	// DisplayName is all an invitee learns before joining. It may be empty when
	// the app has no name to give (xy's legacy boards keep theirs encrypted),
	// which is better than a lie.
	DisplayName(ctx context.Context, q Querier, scopeID int64) (string, error)
	// Names is how the owner's list says who came in and who is waiting.
	Names(ctx context.Context, q Querier, userIDs []int64) (map[int64]string, error)
	// NudgeOwner knocks on the owner's door about a fresh Join Request. It is
	// called after the transaction commits, never inside it, and nothing waits
	// for it: the owner's list is the durable signal.
	NudgeOwner(scopeID, requesterID int64)
}

// Links is the package bound to one app's tables and one kind of scope.
type Links struct {
	// Invites and Uses are the two tables the package owns the SQL on. Their
	// columns are fixed (see the xy schema at internal/server/db.go v23); only
	// the names are the adapter's business.
	Invites string
	Uses    string
	// ScopeID is the column on Invites naming the scope: xy's "board_id".
	ScopeID string
	// ScopeJoin is appended to every select over Invites, which is aliased `i`,
	// so a link whose scope is gone resolves to nothing at all. xy passes the
	// join that drops a deleted board; an app with nothing to check passes "".
	ScopeJoin string

	Scope Scope
}

// Person is one line of the who-came-in list.
type Person struct {
	UserID int64
	Name   string
	At     string
}

// Link is one link as the owner sees it. Joined and Waiting are filled by List.
type Link struct {
	ID               int64
	ScopeID          int64
	Code             string
	Label            string // "" = unlabelled
	CreatedAt        string
	ExpiresAt        string // "" = no expiry
	MaxUses          *int64 // nil = uncapped
	Used             int64
	RequiresApproval bool
	RevokedAt        string // "" = live

	Joined  []Person
	Waiting []Person
}

// State is the link's own condition, before any one caller's history with it.
func (lk Link) State(now time.Time) State {
	switch {
	case lk.RevokedAt != "":
		return Revoked
	case lk.ExpiresAt != "" && !now.Before(parseTime(lk.ExpiresAt)):
		return Expired
	case lk.MaxUses != nil && lk.Used >= *lk.MaxUses:
		return Exhausted
	}
	return Active
}

// Left is how many seats remain, nil on an uncapped link.
func (lk Link) Left() *int64 {
	if lk.MaxUses == nil {
		return nil
	}
	n := *lk.MaxUses - lk.Used
	if n < 0 {
		n = 0
	}
	return &n
}

// Options are a link's own settings at minting time.
type Options struct {
	Label            string
	MaxUses          int64 // 0 = unlimited
	TTLHours         int64 // 0 = no expiry
	RequiresApproval bool
}

// Validate is the check Mint makes, offered separately so an app's HTTP edge
// can refuse a bad request without opening a write transaction for it.
func (o Options) Validate() error {
	if o.MaxUses < 0 || o.TTLHours < 0 || o.TTLHours > MaxTTLHours {
		return ErrLimitsOutOfRange
	}
	if len([]rune(o.Label)) > MaxLabelRunes {
		return ErrLabelTooLong
	}
	return nil
}

// ---- reads ----

func (l Links) selectSQL() string {
	return `
select i.id, i.` + l.ScopeID + `, i.code, i.label, i.created_at, i.expires_at, i.max_uses,
       i.requires_approval, i.revoked_at,
       (select count(*) from ` + l.Uses + ` u where u.invite_id = i.id and u.status = 'joined')
from ` + l.Invites + ` i
` + l.ScopeJoin
}

type scanner interface{ Scan(...any) error }

func scanLink(sc scanner) (Link, error) {
	var (
		lk        Link
		label     sql.NullString
		expiresAt sql.NullString
		maxUses   sql.NullInt64
		revokedAt sql.NullString
	)
	err := sc.Scan(&lk.ID, &lk.ScopeID, &lk.Code, &label, &lk.CreatedAt, &expiresAt,
		&maxUses, &lk.RequiresApproval, &revokedAt, &lk.Used)
	lk.Label, lk.ExpiresAt, lk.RevokedAt = label.String, expiresAt.String, revokedAt.String
	if maxUses.Valid {
		n := maxUses.Int64
		lk.MaxUses = &n
	}
	return lk, err
}

// parseTime reads a stored RFC3339 stamp; an unparseable one reads as long
// past, which fails a link closed rather than open.
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ByID resolves one link, ErrNotFound when it is gone or its scope is.
func (l Links) ByID(ctx context.Context, q Querier, id int64) (Link, error) {
	return l.one(q.QueryRowContext(ctx, l.selectSQL()+` where i.id = ?`, id))
}

// ByCode resolves one link by the code its URL carries.
func (l Links) ByCode(ctx context.Context, q Querier, code string) (Link, error) {
	return l.one(q.QueryRowContext(ctx, l.selectSQL()+` where i.code = ?`, code))
}

func (l Links) one(row *sql.Row) (Link, error) {
	lk, err := scanLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, ErrNotFound
	}
	return lk, err
}

// List is a scope's links newest first, each with who joined through it and who
// is still waiting.
func (l Links) List(ctx context.Context, q Querier, scopeID int64) ([]Link, error) {
	rows, err := q.QueryContext(ctx, l.selectSQL()+`
where i.`+l.ScopeID+` = ? order by i.id desc`, scopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Link{}
	byID := map[int64]int{}
	for rows.Next() {
		lk, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		lk.Joined, lk.Waiting = []Person{}, []Person{}
		byID[lk.ID] = len(out)
		out = append(out, lk)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	return out, l.fillPeople(ctx, q, scopeID, out, byID)
}

// fillPeople hangs each use on its link, in the order the uses were made.
func (l Links) fillPeople(ctx context.Context, q Querier, scopeID int64, out []Link, byID map[int64]int) error {
	useRows, err := q.QueryContext(ctx, `
select u.invite_id, u.user_id, u.status, coalesce(u.decided_at, u.requested_at)
from `+l.Uses+` u
join `+l.Invites+` i on i.id = u.invite_id
where i.`+l.ScopeID+` = ? and u.status in ('joined','pending')
order by u.id`, scopeID)
	if err != nil {
		return err
	}
	defer useRows.Close()
	type use struct {
		link   int64
		person Person
		status string
	}
	var uses []use
	ids := []int64{}
	seen := map[int64]bool{}
	for useRows.Next() {
		var u use
		if err := useRows.Scan(&u.link, &u.person.UserID, &u.status, &u.person.At); err != nil {
			return err
		}
		uses = append(uses, u)
		if !seen[u.person.UserID] {
			seen[u.person.UserID] = true
			ids = append(ids, u.person.UserID)
		}
	}
	if err := useRows.Err(); err != nil {
		return err
	}
	if len(uses) == 0 {
		return nil
	}
	names, err := l.Scope.Names(ctx, q, ids)
	if err != nil {
		return err
	}
	for _, u := range uses {
		i, ok := byID[u.link]
		if !ok {
			continue
		}
		u.person.Name = names[u.person.UserID]
		if u.status == statusJoined {
			out[i].Joined = append(out[i].Joined, u.person)
		} else {
			out[i].Waiting = append(out[i].Waiting, u.person)
		}
	}
	return nil
}

// The three values the uses table's status CHECK allows. They are not States:
// a use's status is the row's, a State is what the link does for someone.
const (
	statusJoined   = "joined"
	statusPending  = "pending"
	statusDeclined = "declined"
)

// ---- the owner's side ----

// Mint writes a new link and answers its id. code is the app's — it is what the
// URL carries, so the app decides how it is generated and how long it is.
func (l Links) Mint(ctx context.Context, tx Tx, scopeID, createdBy int64, code string, opt Options, now time.Time) (int64, error) {
	if err := opt.Validate(); err != nil {
		return 0, err
	}
	var expires, maxUses any
	if opt.TTLHours > 0 {
		expires = stamp(now.Add(time.Duration(opt.TTLHours) * time.Hour))
	}
	if opt.MaxUses > 0 {
		maxUses = opt.MaxUses
	}
	var label any
	if s := strings.TrimSpace(opt.Label); s != "" {
		label = s
	}
	res, err := tx.ExecContext(ctx, `
insert into `+l.Invites+`(`+l.ScopeID+`, code, label, created_by, created_at, expires_at, max_uses, requires_approval)
values(?, ?, ?, ?, ?, ?, ?, ?)`,
		scopeID, code, label, createdBy, stamp(now), expires, maxUses, opt.RequiresApproval)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Revoke kills a link that still had seats. It stays in the owner's list, with
// its history: a link is how its members arrived, not what keeps them in.
func (l Links) Revoke(ctx context.Context, tx Tx, id int64, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
update `+l.Invites+` set revoked_at = ? where id = ? and revoked_at is null`, stamp(now), id)
	return err
}

// Delete drops the row and its use history. The members it admitted stay.
func (l Links) Delete(ctx context.Context, tx Tx, id int64) error {
	if _, err := tx.ExecContext(ctx, `delete from `+l.Uses+` where invite_id = ?`, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `delete from `+l.Invites+` where id = ?`, id)
	return err
}

// Decide settles the one Join Request this person has on this scope. Approving
// still has to fit under the cap: the queue was allowed to grow past it, so the
// seats may have gone to earlier approvals in the meantime.
func (l Links) Decide(ctx context.Context, tx Tx, scopeID, requesterID int64, approve bool, now time.Time) error {
	lk, useID, err := l.pending(ctx, tx, scopeID, requesterID)
	if err != nil {
		return err
	}
	if !approve {
		_, err := tx.ExecContext(ctx, `
update `+l.Uses+` set status = 'declined', decided_at = ? where id = ?`, stamp(now), useID)
		return err
	}
	if lk.MaxUses != nil && lk.Used >= *lk.MaxUses {
		return ErrNoSeatsLeft
	}
	if _, err := tx.ExecContext(ctx, `
update `+l.Uses+` set status = 'joined', decided_at = ? where id = ?`, stamp(now), useID); err != nil {
		return err
	}
	return l.Scope.AddMember(ctx, tx, scopeID, requesterID)
}

// pending finds the one waiting request this person has on this scope, and the
// link it came in through.
func (l Links) pending(ctx context.Context, tx Tx, scopeID, userID int64) (Link, int64, error) {
	lk, err := scanLink(tx.QueryRowContext(ctx, l.selectSQL()+`
join `+l.Uses+` u on u.invite_id = i.id
where i.`+l.ScopeID+` = ? and u.user_id = ? and u.status = 'pending'`, scopeID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return lk, 0, ErrRequestNotFound
	}
	if err != nil {
		return lk, 0, err
	}
	var useID int64
	err = tx.QueryRowContext(ctx, `
select id from `+l.Uses+` where invite_id = ? and user_id = ?`, lk.ID, userID).Scan(&useID)
	return lk, useID, err
}

// ---- the invitee's side ----

// Peek is all an invitee learns before joining: which scope this is, and
// whether the link still works for them.
type Peek struct {
	ScopeID          int64
	ScopeName        string
	State            State
	RequiresApproval bool
}

// PeekCode answers the landing page.
func (l Links) PeekCode(ctx context.Context, q Querier, code string, userID int64, now time.Time) (Peek, error) {
	lk, err := l.ByCode(ctx, q, code)
	if err != nil {
		return Peek{}, err
	}
	state, err := l.stateFor(ctx, q, lk, userID, now)
	if err != nil {
		return Peek{}, err
	}
	name, err := l.Scope.DisplayName(ctx, q, lk.ScopeID)
	if err != nil {
		return Peek{}, err
	}
	return Peek{ScopeID: lk.ScopeID, ScopeName: name, State: state, RequiresApproval: lk.RequiresApproval}, nil
}

// stateFor is what the link does for THIS caller: the link's own state, unless
// their history overrides it.
//
// Waiting is scope-wide, not per-link: one person is one Join Request, and the
// owner decides about a person. Scoped to the link, someone could queue twice
// through two links and be approved into two seats — and a decide would pick
// between their rows arbitrarily.
//
// A refusal and a spent passage are per-link, because both are about this link:
// a decline is final for it (another link lets the owner change their mind),
// and a link that already admitted someone the owner has since removed must not
// quietly let them back in.
func (l Links) stateFor(ctx context.Context, q Querier, lk Link, userID int64, now time.Time) (State, error) {
	member, err := l.Scope.IsMember(ctx, q, lk.ScopeID, userID)
	if err != nil {
		return "", err
	}
	if member {
		return Member, nil
	}
	var waiting int
	if err := q.QueryRowContext(ctx, `
select count(*) from `+l.Uses+` u join `+l.Invites+` i on i.id = u.invite_id
where i.`+l.ScopeID+` = ? and u.user_id = ? and u.status = 'pending'`, lk.ScopeID, userID).Scan(&waiting); err != nil {
		return "", err
	}
	if waiting > 0 {
		return Pending, nil
	}
	var status string
	err = q.QueryRowContext(ctx, `
select status from `+l.Uses+` where invite_id = ? and user_id = ?`, lk.ID, userID).Scan(&status)
	if err == nil {
		if status == statusDeclined {
			return Declined, nil
		}
		return Spent, nil // 'joined', and they are not a member: the owner removed them
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return lk.State(now), nil
}

// Result is what following a link did.
type Result struct {
	ScopeID int64
	State   State // Member or Pending
	// Nudge says a fresh Join Request is waiting. The caller commits its
	// transaction and then calls Nudge — the owner must not hear about a join
	// that rolled back.
	Nudge bool
}

// Join follows a link, inside the caller's WRITE transaction: the state is
// re-read in it, so two people racing the last seat cannot both take it.
// It answers Refused when the link will not admit them, ErrNotFound when there
// is no such link (or its scope is gone).
func (l Links) Join(ctx context.Context, tx Tx, code string, userID int64, now time.Time) (Result, error) {
	lk, err := l.ByCode(ctx, tx, code)
	if err != nil {
		return Result{}, err
	}
	out := Result{ScopeID: lk.ScopeID}
	state, err := l.stateFor(ctx, tx, lk, userID, now)
	if err != nil {
		return out, err
	}
	switch state {
	case Member, Pending:
		out.State = state
		return out, nil
	case Active:
	default:
		return out, &Refused{State: state}
	}
	status := statusJoined
	if lk.RequiresApproval {
		status = statusPending
	}
	if _, err := tx.ExecContext(ctx, `
insert into `+l.Uses+`(invite_id, user_id, status, requested_at, decided_at)
values(?, ?, ?, ?, ?)`, lk.ID, userID, status, stamp(now), nullIf(status == statusPending, stamp(now))); err != nil {
		return out, err
	}
	if status == statusPending {
		out.State, out.Nudge = Pending, true
		return out, nil
	}
	out.State = Member
	return out, l.Scope.AddMember(ctx, tx, lk.ScopeID, userID)
}

// Nudge tells the scope's owner about a Join Request, after the commit.
func (l Links) Nudge(scopeID, requesterID int64) { l.Scope.NudgeOwner(scopeID, requesterID) }

// nullIf writes NULL when the condition holds — a pending row has no decision yet.
func nullIf(cond bool, v string) any {
	if cond {
		return nil
	}
	return v
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }
