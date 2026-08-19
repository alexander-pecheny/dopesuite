// Package tglogin is the server side of the Telegram login handshake the
// shared login page drives (dopeuikit login-model.ts): Start mints a code the
// visitor forwards to the bot, Resolve polls it once the bot has filled in who
// sent it (tgbridge.ConsumeRegisterSQL), and Claim settles a brand-new telegram
// account on a username. The state machine, its SQL on telegram_login_codes
// and the closed status set live here once; each app brings its write
// transaction, its users table and its error text.
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

// Tx is what the handshake needs from the app's write transaction.
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

// Users is the app's users table. The handshake writes no SQL against it: the
// two apps' tables differ (dope has is_system and password_salt), and an app
// may refuse a username Create would otherwise hand out by returning its own
// error, which Claim passes through unchanged.
type Users interface {
	ByTelegram(ctx context.Context, tx Tx, telegramUserID int64) (Account, bool, error)
	ByUsername(ctx context.Context, tx Tx, username string) (Account, bool, error)
	Create(ctx context.Context, tx Tx, id Identity, username string, now time.Time) (int64, error)
	// Attach writes the telegram identity onto an existing account — on a
	// known telegram's login to keep the display fields current, on a password
	// proof to link it.
	Attach(ctx context.Context, tx Tx, userID int64, id Identity, now time.Time) error
}

// Outcome is one poll or claim's answer: a status, the username it settled on,
// and — when Status is Ready — the session token the app sets as its cookie.
type Outcome struct {
	Status   string
	Username *string
	Token    string
}

// StartResult is a minted code and when it lapses.
type StartResult struct {
	Code      string
	ExpiresAt time.Time
}

// Handshake is the state machine over one app's users table.
type Handshake struct{ Users Users }

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Start mints a register code valid for session.TelegramAuthLifetime, reaping
// lapsed codes first so consumed-but-abandoned rows don't linger as replay fodder.
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

// Resolve polls a code. A known telegram logs straight in (Ready, with the code
// burned so it can't mint a second session); a brand-new one answers
// ChooseUsername for Claim to finish. Expiry bounds the whole handshake,
// consumed or not, so a code leaked via the status URL can't be replayed into a
// session once it lapses.
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
	return h.login(ctx, tx, acct.ID, code, firstNonNull(acct.Username, id.Username), now)
}

// Claim finishes a brand-new telegram account: the visitor picks a username.
// Free → create and log in. An existing password account → link it once they
// prove the password (PasswordRequired until they do; ErrWrongPassword when
// they fail). Held by a passwordless account → UsernameTaken. The caller has
// already validated the username's shape.
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

	// This telegram may already resolve to an account (double-submit / race).
	if acct, found, err := h.Users.ByTelegram(ctx, tx, id.TelegramUserID); err != nil {
		return Outcome{}, err
	} else if found {
		return h.login(ctx, tx, acct.ID, code, firstNonNull(acct.Username, sql.NullString{String: username, Valid: true}), now)
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

// login mints the session and burns the code so it can't be replayed.
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

func firstNonNull(vals ...sql.NullString) *string {
	for _, v := range vals {
		if v.Valid && v.String != "" {
			s := v.String
			return &s
		}
	}
	return nil
}
