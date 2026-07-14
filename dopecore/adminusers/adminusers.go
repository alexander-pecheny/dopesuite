// Package adminusers holds the app-agnostic half of the /admin "bulk create
// users" tooling shared by xy and dope: the admin gate, the one-time password
// generator, the textarea parser, the page data types, and the create loop
// behind a store interface. The pages themselves stay in each app — they speak
// each app's own ui vocabulary, which dopecore does not depend on.
package adminusers

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"os"
	"strings"

	"pecheny.me/dopecore/authcred"
	"pecheny.me/dopecore/session"
)

// AdminUsername gates the /admin tooling. Defaults to "pecheny"; override with
// envVar (XY_ADMIN_USER / DOPE_ADMIN_USER) for other deployments or tests.
func AdminUsername(envVar string) string {
	if v := strings.TrimSpace(os.Getenv(envVar)); v != "" {
		return v
	}
	return "pecheny"
}

// RequireAdmin resolves the session via lookup and confirms it belongs to the
// configured admin. On failure it writes the response itself — a redirect to
// /login when logged out, otherwise a 404 so the page's existence isn't
// revealed to authenticated non-admins — and returns false.
func RequireAdmin(w http.ResponseWriter, r *http.Request, envVar string, lookup func() (session.User, bool)) (session.User, bool) {
	user, ok := lookup()
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return session.User{}, false
	}
	if !user.Username.Valid || user.Username.String != AdminUsername(envVar) {
		http.NotFound(w, r)
		return session.User{}, false
	}
	return user, true
}

// GeneratedPasswordAlphabet omits look-alike characters (0/O, 1/l/I) so the
// one-time passwords can be read aloud or retyped without ambiguity.
const GeneratedPasswordAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
const GeneratedPasswordLen = 12

func NewRandomPassword() (string, error) {
	buf := make([]byte, GeneratedPasswordLen)
	max := big.NewInt(int64(len(GeneratedPasswordAlphabet)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = GeneratedPasswordAlphabet[n.Int64()]
	}
	return string(buf), nil
}

// ParseUsernameLines splits the textarea input into trimmed, de-duplicated
// usernames, preserving first-seen order and dropping blank lines.
func ParseUsernameLines(raw string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

type CreatedUser struct {
	Username string
	Password string
}

type UserError struct {
	Username string
	Reason   string
}

type CreateUsersData struct {
	Submitted bool
	Created   []CreatedUser
	Skipped   []string
	Errors    []UserError
}

// Copyable returns the created credentials as tab-separated lines, ready to
// paste into a message to hand out to each new user.
func (d CreateUsersData) Copyable() string {
	var b strings.Builder
	for _, u := range d.Created {
		b.WriteString(u.Username)
		b.WriteString("\t")
		b.WriteString(u.Password)
		b.WriteString("\n")
	}
	return b.String()
}

// ErrUserExists is what a Store's InsertUser returns when the row lost a race
// against a concurrent insert: the username is reported as skipped, not failed.
// Each app maps its own driver/schema's unique-violation onto it.
var ErrUserExists = errors.New("adminusers: user already exists")

// Store is the app's half of the create loop: its schema, its SQL, its
// transaction.
type Store interface {
	UserExists(ctx context.Context, username string) (bool, error)
	InsertUser(ctx context.Context, username, passwordHash string) error
}

// ErrorPolicy decides what a failing row does to the batch. The two apps differ
// here on purpose: xy runs the batch inside one write transaction and aborts it
// whole (AbortOnRowError), dope commits the rows that worked and reports the
// rest (CollectRowErrors).
type ErrorPolicy int

const (
	AbortOnRowError ErrorPolicy = iota
	CollectRowErrors
)

// Creator runs the bulk-create loop: validate → skip if the user exists →
// random password → hash → insert.
type Creator struct {
	Store    Store
	Validate func(username string) bool
	Policy   ErrorPolicy
}

// Create processes usernames in order. An invalid username is always a
// collected per-row error (never fatal, in both apps). Any other failure obeys
// Policy: AbortOnRowError returns it (the caller rolls its transaction back),
// CollectRowErrors records it against the row and moves on — in which case the
// returned error is always nil.
func (c Creator) Create(ctx context.Context, usernames []string) (CreateUsersData, error) {
	data := CreateUsersData{Submitted: true}
	for _, name := range usernames {
		if !c.Validate(name) {
			data.Errors = append(data.Errors, UserError{Username: name, Reason: "недопустимый логин"})
			continue
		}
		exists, err := c.Store.UserExists(ctx, name)
		if err != nil {
			if c.Policy == AbortOnRowError {
				return data, err
			}
			data.Errors = append(data.Errors, UserError{Username: name, Reason: err.Error()})
			continue
		}
		if exists {
			data.Skipped = append(data.Skipped, name)
			continue
		}
		password, err := NewRandomPassword()
		if err != nil {
			if c.Policy == AbortOnRowError {
				return data, err
			}
			data.Errors = append(data.Errors, UserError{Username: name, Reason: err.Error()})
			continue
		}
		hash, err := authcred.HashPassword(password)
		if err != nil {
			if c.Policy == AbortOnRowError {
				return data, err
			}
			data.Errors = append(data.Errors, UserError{Username: name, Reason: err.Error()})
			continue
		}
		if err := c.Store.InsertUser(ctx, name, hash); err != nil {
			if errors.Is(err, ErrUserExists) {
				data.Skipped = append(data.Skipped, name)
				continue
			}
			if c.Policy == AbortOnRowError {
				return data, err
			}
			data.Errors = append(data.Errors, UserError{Username: name, Reason: err.Error()})
			continue
		}
		data.Created = append(data.Created, CreatedUser{Username: name, Password: password})
	}
	return data, nil
}
