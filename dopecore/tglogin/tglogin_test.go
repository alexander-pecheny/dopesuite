package tglogin

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"pecheny.me/dopecore/authcred"
)

// memUsers is an app's users table in a map: enough to drive the state machine
// and to inject the refusals an app expresses through Create/Attach.
type memUsers struct {
	accts     map[int64]*Account
	telegrams map[int64]int64 // telegram user id → account id
	next      int64
	createErr error
	attachErr error
}

func newUsers() *memUsers {
	return &memUsers{accts: map[int64]*Account{}, telegrams: map[int64]int64{}}
}

func (m *memUsers) add(username, hash string) int64 {
	m.next++
	a := &Account{ID: m.next}
	if username != "" {
		a.Username = sql.NullString{String: username, Valid: true}
	}
	if hash != "" {
		a.PasswordHash = sql.NullString{String: hash, Valid: true}
	}
	m.accts[a.ID] = a
	return a.ID
}

func (m *memUsers) ByTelegram(_ context.Context, _ Tx, tg int64) (Account, bool, error) {
	id, ok := m.telegrams[tg]
	if !ok {
		return Account{}, false, nil
	}
	return *m.accts[id], true, nil
}

func (m *memUsers) ByUsername(_ context.Context, _ Tx, username string) (Account, bool, error) {
	for _, a := range m.accts {
		if a.Username.String == username {
			return *a, true, nil
		}
	}
	return Account{}, false, nil
}

func (m *memUsers) Create(_ context.Context, _ Tx, id Identity, username string, _ time.Time) (int64, error) {
	if m.createErr != nil {
		return 0, m.createErr
	}
	if _, taken, _ := m.ByUsername(nil, nil, username); taken {
		return 0, errors.New("UNIQUE constraint failed: users.username")
	}
	uid := m.add(username, "")
	m.telegrams[id.TelegramUserID] = uid
	return uid, nil
}

func (m *memUsers) Attach(_ context.Context, _ Tx, userID int64, id Identity, _ time.Time) error {
	if m.attachErr != nil {
		return m.attachErr
	}
	if owner, ok := m.telegrams[id.TelegramUserID]; ok && owner != userID {
		return errors.New("UNIQUE constraint failed: users.telegram_user_id")
	}
	m.telegrams[id.TelegramUserID] = userID
	return nil
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
create table telegram_login_codes(id integer primary key, code text not null unique, kind text not null,
  telegram_user_id integer, telegram_username text, telegram_name text,
  created_at text not null, expires_at text not null, consumed_at text);
create table sessions(id integer primary key, user_id integer not null, token_hash text not null unique,
  created_at text not null, expires_at text not null, last_seen_at text not null);`); err != nil {
		t.Fatal(err)
	}
	return db
}

type fixture struct {
	t     *testing.T
	db    *sql.DB
	users *memUsers
	h     Handshake
	now   time.Time
}

func newFixture(t *testing.T) *fixture {
	u := newUsers()
	return &fixture{t: t, db: openDB(t), users: u, h: Handshake{Users: u}, now: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)}
}

// tx runs fn in a transaction and commits it, like both apps' write wrappers.
func (f *fixture) tx(fn func(tx *sql.Tx) error) {
	f.t.Helper()
	tx, err := f.db.Begin()
	if err != nil {
		f.t.Fatal(err)
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		f.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) start() string {
	var code string
	f.tx(func(tx *sql.Tx) error {
		r, err := f.h.Start(context.Background(), tx, f.now)
		code = r.Code
		return err
	})
	return code
}

// bot is what tgbridge.ConsumeRegisterSQL does when the visitor forwards the code.
func (f *fixture) bot(code string, tg int64, username string) {
	f.tx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`update telegram_login_codes set telegram_user_id = ?, telegram_username = ?, consumed_at = ? where code = ?`,
			tg, username, rfc3339(f.now), code)
		return err
	})
}

func (f *fixture) resolve(code string) Outcome {
	var out Outcome
	f.tx(func(tx *sql.Tx) error {
		var err error
		out, err = f.h.Resolve(context.Background(), tx, code, f.now)
		return err
	})
	return out
}

func (f *fixture) claim(code, username, password string) (Outcome, error) {
	var out Outcome
	var cerr error
	f.tx(func(tx *sql.Tx) error {
		out, cerr = f.h.Claim(context.Background(), tx, code, username, password, f.now)
		return nil
	})
	return out, cerr
}

func (f *fixture) sessions() int {
	var n int
	if err := f.db.QueryRow(`select count(*) from sessions`).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func want(t *testing.T, out Outcome, status string, username string) {
	t.Helper()
	if out.Status != status {
		t.Fatalf("status = %q, want %q", out.Status, status)
	}
	if username != "" && (out.Username == nil || *out.Username != username) {
		t.Fatalf("username = %v, want %q", out.Username, username)
	}
	if (out.Token != "") != (status == Ready) {
		t.Fatalf("token %q for status %q", out.Token, status)
	}
}

func TestResolveStates(t *testing.T) {
	f := newFixture(t)
	want(t, f.resolve("NOPE"), NotFound, "")
	code := f.start()
	want(t, f.resolve(code), Pending, "")
	want(t, f.resolve(" "+code+" "), Pending, "") // the page may send it unnormalised

	f.bot(code, 555, "tg_alice")
	want(t, f.resolve(code), ChooseUsername, "")

	// A known telegram logs straight in, once: the code is burned.
	uid := f.users.add("alice", "")
	f.users.telegrams[555] = uid
	want(t, f.resolve(code), Ready, "alice")
	want(t, f.resolve(code), NotFound, "")
	if f.sessions() != 1 {
		t.Fatalf("sessions = %d", f.sessions())
	}

	// Expiry bounds the whole handshake, consumed or not.
	for _, consumed := range []bool{false, true} {
		code := f.start()
		if consumed {
			f.bot(code, 555, "tg_alice")
		}
		f.now = f.now.Add(2 * time.Minute)
		want(t, f.resolve(code), Expired, "")
		f.now = f.now.Add(-2 * time.Minute)
	}
}

func TestStartReapsLapsedCodes(t *testing.T) {
	f := newFixture(t)
	old := f.start()
	f.now = f.now.Add(2 * time.Minute)
	f.start()
	var n int
	f.db.QueryRow(`select count(*) from telegram_login_codes where code = ?`, old).Scan(&n)
	if n != 0 {
		t.Fatal("lapsed code survived a Start")
	}
}

func TestClaimNewUsername(t *testing.T) {
	f := newFixture(t)
	code := f.start()
	if _, err := f.claim(code, "bob", ""); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("unconsumed code: %v", err)
	}
	f.bot(code, 700, "tg_bob")
	out, err := f.claim(code, "bob", "")
	if err != nil {
		t.Fatal(err)
	}
	want(t, out, Ready, "bob")
	if f.users.telegrams[700] == 0 {
		t.Fatal("account not created for the telegram")
	}
	// Burned: a replay finds nothing, and mints nothing.
	if _, err := f.claim(code, "bob", ""); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("replay: %v", err)
	}
	if f.sessions() != 1 {
		t.Fatalf("sessions = %d", f.sessions())
	}
}

func TestClaimDoubleSubmitLogsTheSameAccountIn(t *testing.T) {
	f := newFixture(t)
	code := f.start()
	f.bot(code, 701, "tg")
	uid := f.users.add("carol", "")
	f.users.telegrams[701] = uid
	out, err := f.claim(code, "whatever", "")
	if err != nil {
		t.Fatal(err)
	}
	want(t, out, Ready, "carol")
}

func TestClaimTakenByPasswordlessAccount(t *testing.T) {
	f := newFixture(t)
	f.users.add("taken", "")
	code := f.start()
	f.bot(code, 702, "tg")
	out, err := f.claim(code, "taken", "")
	if err != nil {
		t.Fatal(err)
	}
	want(t, out, UsernameTaken, "taken")
	if f.sessions() != 0 {
		t.Fatal("a session was minted")
	}
}

func TestClaimLinksPasswordAccountOnProof(t *testing.T) {
	f := newFixture(t)
	hash, _ := authcred.HashPassword("correct-horse")
	uid := f.users.add("alice", hash)
	code := f.start()
	f.bot(code, 703, "tg")

	out, err := f.claim(code, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	want(t, out, PasswordRequired, "alice")
	if _, err := f.claim(code, "alice", "nope"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	out, err = f.claim(code, "alice", "correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	want(t, out, Ready, "alice")
	if f.users.telegrams[703] != uid {
		t.Fatal("telegram not attached")
	}
}

func TestClaimLegacyHashAlsoProves(t *testing.T) {
	f := newFixture(t)
	uid := f.users.add("dan", authcred.LegacySHA256Password("pw123456", "salt"))
	f.users.accts[uid].PasswordSalt = sql.NullString{String: "salt", Valid: true}
	code := f.start()
	f.bot(code, 704, "tg")
	out, err := f.claim(code, "dan", "pw123456")
	if err != nil {
		t.Fatal(err)
	}
	want(t, out, Ready, "dan")
}

func TestClaimMapsAttachUniqueViolationToErrTelegramLinked(t *testing.T) {
	f := newFixture(t)
	hash, _ := authcred.HashPassword("correct-horse")
	f.users.add("alice", hash)
	f.users.attachErr = errors.New("UNIQUE constraint failed: users.telegram_user_id")
	code := f.start()
	f.bot(code, 705, "tg")
	if _, err := f.claim(code, "alice", "correct-horse"); !errors.Is(err, ErrTelegramLinked) {
		t.Fatalf("err = %v", err)
	}
}

func TestClaimPassesTheAppsOwnRefusalThrough(t *testing.T) {
	f := newFixture(t)
	reserved := errors.New("этот логин зарезервирован")
	f.users.createErr = reserved
	code := f.start()
	f.bot(code, 706, "tg")
	if _, err := f.claim(code, "admin", ""); !errors.Is(err, reserved) {
		t.Fatalf("err = %v", err)
	}
}
