// Package tglogin is the server side of the Telegram login handshake the
// shared login page drives (dopeuikit login-model.ts): Start mints a code the
// visitor forwards to the bot, Resolve polls it once the bot has filled in who
// sent it (tgbridge.ConsumeRegisterSQL), Claim settles a brand-new telegram on
// a username. Each app brings its write transaction, its users table and its
// error text; the state machine and the SQL on telegram_login_codes live here.
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

// Claim's refusals. The app maps each to its own HTTP status and wording.
var (
	ErrCodeNotFound   = errors.New("code not found")
	ErrWrongPassword  = errors.New("wrong password")
	ErrTelegramLinked = errors.New("telegram already linked to another account")
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
// sha256 scheme's salt; empty for bcrypt.
type Account struct {
	ID           int64
	Username     sql.NullString
	PasswordHash sql.NullString
	PasswordSalt sql.NullString
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

// Outcome is one poll or claim's answer; Token is set with Ready, for the cookie.
type Outcome struct {
	Status   string
	Username *string
	Token    string
}

type StartResult struct {
	Code      string
	ExpiresAt time.Time
}

type Handshake struct{ Users Users }

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

// Resolve polls a code: a known telegram logs straight in, a new one answers
// ChooseUsername for Claim. Expiry bounds the handshake consumed or not, so a
// code leaked via the status URL can't be replayed into a session once it lapses.
func (h Handshake) Resolve(ctx context.Context, tx Tx, code string, now time.Time) (Outcome, error) {
	code = normalise(code)
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
		return Outcome{Status: NotFound}, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	if expiry, _ := time.Parse(time.RFC3339, expiresAt); now.After(expiry) {
		return Outcome{Status: Expired}, nil
	}
	if !consumedAt.Valid || !tgUserID.Valid {
		return Outcome{Status: Pending}, nil
	}
	id.TelegramUserID = tgUserID.Int64
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

// login mints the session and burns the code.
func (h Handshake) login(ctx context.Context, tx Tx, userID int64, code string, username *string, now time.Time) (Outcome, error) {
	token, err := authcred.CreateSession(ctx, tx, userID, now)
	if err != nil {
		return Outcome{}, err
	}
	if _, err := tx.ExecContext(ctx, `delete from telegram_login_codes where code = ?`, code); err != nil {
		return Outcome{}, err
	}
	return Outcome{Status: Ready, Username: username, Token: token}, nil
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
