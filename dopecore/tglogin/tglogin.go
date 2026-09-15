// Package tglogin is the server side of the Telegram login handshake the
// shared login page drives (dopeuikit login-model.ts): Start mints a code the
// visitor forwards to the bot, Resolve polls it once the bot has filled in who
// sent it (tgbridge.ConsumeRegisterSQL), Claim settles a brand-new telegram on
// a username. Link is the same code read by somebody who is already logged in:
// it settles the telegram on the account they are in, and mints no session.
// Each app brings its write transaction, its users table and its error text;
// the state machine and the SQL on telegram_login_codes live here.
package tglogin

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"pecheny.me/dopecore/authcred"
	"pecheny.me/dopecore/session"
	"pecheny.me/dopecore/sqlitex"
)

// The statuses the login page understands (login-model.ts).
const (
	Pending          = "pending"
	Ready            = "ready"
	ChooseUsername   = "choose_username"
	Expired          = "expired"
	NotFound         = "not_found"
	UsernameTaken    = "username_taken"
	PasswordRequired = "password_required"
)

// Linked is Link's success, and is not one of the login page's statuses: the
// caller already had a session, so there is nothing to redirect to.
const Linked = "linked"

// Claim's and Link's refusals. The app maps each to its own HTTP status and
// wording.
var (
	ErrCodeNotFound   = errors.New("code not found")
	ErrWrongPassword  = errors.New("wrong password")
	ErrTelegramLinked = errors.New("telegram already linked to another account")
	ErrAccountLinked  = errors.New("account already has a telegram")

	// errNoAccount is Link's "can't happen": a live session naming a user row
	// that is not there. Unexported because no app has a sensible answer to it
	// beyond the generic one every internal error gets.
	errNoAccount = errors.New("tglogin: no such account")
	errNoReader  = errors.New("tglogin: Link needs Handshake.Accounts")
)

type Tx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Identity is who the bot recorded on a code.
type Identity struct {
	TelegramUserID int64
	Username       sql.NullString
	Name           sql.NullString
}

// Account is a users row as the handshake needs it. PasswordSalt is the legacy
// sha256 scheme's salt; empty for bcrypt. TelegramUserID is read only by the
// Accounts lookup Link uses — the login flow finds accounts BY it and never
// needs to read it back.
type Account struct {
	ID             int64
	Username       sql.NullString
	PasswordHash   sql.NullString
	PasswordSalt   sql.NullString
	TelegramUserID sql.NullInt64
}

// Users is the app's users table; the handshake writes no SQL against it
// because the two apps' tables differ (dope has is_system and password_salt).
// Create's unique violation reads as UsernameTaken; any other error it returns
// — an app refusing a reserved name — passes through Claim unchanged. Attach
// writes the identity onto an existing account, on login and on a password proof.
type Users interface {
	ByTelegram(ctx context.Context, tx Tx, telegramUserID int64) (Account, bool, error)
	ByUsername(ctx context.Context, tx Tx, username string) (Account, bool, error)
	Create(ctx context.Context, tx Tx, id Identity, username string, now time.Time) (int64, error)
	Attach(ctx context.Context, tx Tx, userID int64, id Identity, now time.Time) error
}

// Outcome is one poll, claim or link's answer; Token is set with Ready, for the
// cookie, and Telegram with Linked, so the page can name the handle it just
// attached without asking again.
type Outcome struct {
	Status   string
	Username *string
	Token    string
	Telegram *string
}

type StartResult struct {
	Code      string
	ExpiresAt time.Time
}

// Accounts is the one read Link needs and the login flow does not: the account
// behind a live session, to see whether it already carries a telegram. It is a
// field of its own rather than a method on Users because only an app that
// offers linking from a logged-in page has anything to answer it with.
type Accounts interface {
	ByID(ctx context.Context, tx Tx, userID int64) (Account, bool, error)
}

type Handshake struct {
	Users    Users
	Accounts Accounts // required by Link, unused by the login flow
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Start mints a register code, reaping lapsed codes first so consumed-but-
// abandoned rows don't linger as replay fodder.
func (h Handshake) Start(ctx context.Context, tx Tx, now time.Time) (StartResult, error) {
	if _, err := tx.ExecContext(ctx, `delete from telegram_login_codes where expires_at < ?`, rfc3339(now)); err != nil {
		return StartResult{}, err
	}
	expires := now.Add(session.TelegramAuthLifetime)
	for range 3 {
		code, err := authcred.NewTelegramAuthCode()
		if err != nil {
			return StartResult{}, err
		}
		_, err = tx.ExecContext(ctx, `
insert into telegram_login_codes(code, kind, created_at, expires_at)
values(?, 'register', ?, ?)`, code, rfc3339(now), rfc3339(expires))
		if err == nil {
			return StartResult{Code: code, ExpiresAt: expires}, nil
		}
		if !sqlitex.IsUniqueViolation(err) {
			return StartResult{}, err
		}
	}
	return StartResult{}, errors.New("could not allocate code")
}

// poll reads a register code and says who is on it. A status other than the
// empty string is the whole answer — not there, lapsed, or the bot has not
// written back yet — and Identity is only filled in when that status is empty.
// Expiry bounds the handshake consumed or not, so a code leaked via the status
// URL can't be replayed into a session, or onto an account, once it lapses.
func (h Handshake) poll(ctx context.Context, tx Tx, code string, now time.Time) (Identity, string, error) {
	var (
		id         Identity
		tgUserID   sql.NullInt64
		expiresAt  string
		consumedAt sql.NullString
	)
	err := tx.QueryRowContext(ctx, `
select telegram_user_id, telegram_username, telegram_name, expires_at, consumed_at
from telegram_login_codes where code = ? and kind = 'register'`, code).
		Scan(&tgUserID, &id.Username, &id.Name, &expiresAt, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return id, NotFound, nil
	}
	if err != nil {
		return id, "", err
	}
	if expiry, _ := time.Parse(time.RFC3339, expiresAt); now.After(expiry) {
		return id, Expired, nil
	}
	if !consumedAt.Valid || !tgUserID.Valid {
		return id, Pending, nil
	}
	id.TelegramUserID = tgUserID.Int64
	return id, "", nil
}

// Resolve polls a code for the login page: a known telegram logs straight in, a
// new one answers ChooseUsername for Claim.
func (h Handshake) Resolve(ctx context.Context, tx Tx, code string, now time.Time) (Outcome, error) {
	code = normalise(code)
	id, status, err := h.poll(ctx, tx, code, now)
	if err != nil || status != "" {
		return Outcome{Status: status}, err
	}
	acct, found, err := h.Users.ByTelegram(ctx, tx, id.TelegramUserID)
	if err != nil {
		return Outcome{}, err
	}
	if !found {
		return Outcome{Status: ChooseUsername}, nil
	}
	if err := h.Users.Attach(ctx, tx, acct.ID, id, now); err != nil {
		return Outcome{}, err
	}
	return h.login(ctx, tx, acct.ID, code, nameOf(acct, id.Username.String), now)
}

// Claim settles a new telegram on a username the caller has validated: free →
// create and log in; a password account → link once the password is proven;
// a passwordless account → UsernameTaken.
func (h Handshake) Claim(ctx context.Context, tx Tx, code, username, password string, now time.Time) (Outcome, error) {
	code = normalise(code)
	var (
		id       Identity
		tgUserID sql.NullInt64
	)
	err := tx.QueryRowContext(ctx, `
select telegram_user_id, telegram_username, telegram_name
from telegram_login_codes
where code = ? and kind = 'register' and consumed_at is not null and expires_at > ?`, code, rfc3339(now)).
		Scan(&tgUserID, &id.Username, &id.Name)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !tgUserID.Valid) {
		return Outcome{}, ErrCodeNotFound
	}
	if err != nil {
		return Outcome{}, err
	}
	id.TelegramUserID = tgUserID.Int64

	// A double-submit: this telegram already resolved to an account.
	if acct, found, err := h.Users.ByTelegram(ctx, tx, id.TelegramUserID); err != nil {
		return Outcome{}, err
	} else if found {
		return h.login(ctx, tx, acct.ID, code, nameOf(acct, username), now)
	}

	acct, found, err := h.Users.ByUsername(ctx, tx, username)
	if err != nil {
		return Outcome{}, err
	}
	switch {
	case !found:
		uid, err := h.Users.Create(ctx, tx, id, username, now)
		if sqlitex.IsUniqueViolation(err) {
			return Outcome{Status: UsernameTaken, Username: &username}, nil
		}
		if err != nil {
			return Outcome{}, err
		}
		return h.login(ctx, tx, uid, code, &username, now)
	case acct.PasswordHash.Valid && acct.PasswordHash.String != "":
		if password == "" {
			return Outcome{Status: PasswordRequired, Username: &username}, nil
		}
		ok, _, err := authcred.VerifyPasswordUpgrading(acct.PasswordHash.String, acct.PasswordSalt.String, password)
		if err != nil {
			return Outcome{}, err
		}
		if !ok {
			return Outcome{}, ErrWrongPassword
		}
		if err := h.Users.Attach(ctx, tx, acct.ID, id, now); err != nil {
			if sqlitex.IsUniqueViolation(err) {
				return Outcome{}, ErrTelegramLinked
			}
			return Outcome{}, err
		}
		return h.login(ctx, tx, acct.ID, code, &username, now)
	default:
		return Outcome{Status: UsernameTaken, Username: &username}, nil
	}
}

// Link settles the telegram on a code onto the account the caller is already
// logged into — the one way in that starts from a session rather than making
// one. It refuses a telegram that is somebody else's (ErrTelegramLinked) and an
// account that already has one (ErrAccountLinked): an account carries at most
// one telegram, and moving one is not this step's business. The code is burned
// like any other, and no session is minted — the caller already has the only
// one that should exist for this act.
func (h Handshake) Link(ctx context.Context, tx Tx, code string, userID int64, now time.Time) (Outcome, error) {
	if h.Accounts == nil {
		return Outcome{}, errNoReader
	}
	code = normalise(code)
	id, status, err := h.poll(ctx, tx, code, now)
	if err != nil || status != "" {
		return Outcome{Status: status}, err
	}
	acct, found, err := h.Accounts.ByID(ctx, tx, userID)
	if err != nil {
		return Outcome{}, err
	}
	if !found {
		return Outcome{}, errNoAccount
	}
	if acct.TelegramUserID.Valid {
		return Outcome{}, ErrAccountLinked
	}
	// The account has no telegram, so anything this one already answers to is
	// another account — including, on a double submit, one this very code made.
	if _, taken, err := h.Users.ByTelegram(ctx, tx, id.TelegramUserID); err != nil {
		return Outcome{}, err
	} else if taken {
		return Outcome{}, ErrTelegramLinked
	}
	if err := h.Users.Attach(ctx, tx, userID, id, now); err != nil {
		if sqlitex.IsUniqueViolation(err) {
			return Outcome{}, ErrTelegramLinked
		}
		return Outcome{}, err
	}
	if err := h.burn(ctx, tx, code); err != nil {
		return Outcome{}, err
	}
	out := Outcome{Status: Linked, Username: nameOf(acct, "")}
	if handle := strings.TrimPrefix(id.Username.String, "@"); handle != "" {
		out.Telegram = &handle
	}
	return out, nil
}

// login mints the session and burns the code.
func (h Handshake) login(ctx context.Context, tx Tx, userID int64, code string, username *string, now time.Time) (Outcome, error) {
	token, err := authcred.CreateSession(ctx, tx, userID, now)
	if err != nil {
		return Outcome{}, err
	}
	if err := h.burn(ctx, tx, code); err != nil {
		return Outcome{}, err
	}
	return Outcome{Status: Ready, Username: username, Token: token}, nil
}

// burn forgets a code once it has done its one job, so nothing can replay it.
func (h Handshake) burn(ctx context.Context, tx Tx, code string) error {
	_, err := tx.ExecContext(ctx, `delete from telegram_login_codes where code = ?`, code)
	return err
}

func normalise(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }

// nameOf is the account's username, else fallback, else nil.
func nameOf(acct Account, fallback string) *string {
	name := fallback
	if acct.Username.Valid && acct.Username.String != "" {
		name = acct.Username.String
	}
	if name == "" {
		return nil
	}
	return &name
}
