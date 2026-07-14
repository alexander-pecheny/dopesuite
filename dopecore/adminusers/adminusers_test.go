package adminusers

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeStore struct {
	existing  map[string]bool
	inserted  []string
	existsErr error
	insertErr error
}

func (f *fakeStore) UserExists(_ context.Context, username string) (bool, error) {
	if f.existsErr != nil {
		return false, f.existsErr
	}
	return f.existing[username], nil
}

func (f *fakeStore) InsertUser(_ context.Context, username, hash string) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	if hash == "" {
		return errors.New("empty hash")
	}
	f.inserted = append(f.inserted, username)
	return nil
}

func alwaysValid(string) bool { return true }

func TestParseUsernameLines(t *testing.T) {
	got := ParseUsernameLines(" a \n\nb\na\n  \nc")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestNewRandomPassword(t *testing.T) {
	p, err := NewRandomPassword()
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != GeneratedPasswordLen {
		t.Fatalf("len = %d, want %d", len(p), GeneratedPasswordLen)
	}
	for _, r := range p {
		if !strings.ContainsRune(GeneratedPasswordAlphabet, r) {
			t.Fatalf("password %q has out-of-alphabet rune %q", p, r)
		}
	}
}

func TestCreateSkipsExistingAndReportsInvalid(t *testing.T) {
	store := &fakeStore{existing: map[string]bool{"boss": true}}
	c := Creator{Store: store, Validate: func(s string) bool { return s != "bad name" }, Policy: CollectRowErrors}
	data, err := c.Create(context.Background(), []string{"alice", "boss", "bad name"})
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Created) != 1 || data.Created[0].Username != "alice" {
		t.Fatalf("created = %v", data.Created)
	}
	if len(data.Skipped) != 1 || data.Skipped[0] != "boss" {
		t.Fatalf("skipped = %v", data.Skipped)
	}
	if len(data.Errors) != 1 || data.Errors[0].Username != "bad name" {
		t.Fatalf("errors = %v", data.Errors)
	}
	if !data.Submitted {
		t.Fatal("Submitted not set")
	}
	if want := "alice\t" + data.Created[0].Password + "\n"; data.Copyable() != want {
		t.Fatalf("Copyable() = %q, want %q", data.Copyable(), want)
	}
}

func TestCreateInsertRaceCountsAsSkipped(t *testing.T) {
	store := &fakeStore{insertErr: ErrUserExists}
	c := Creator{Store: store, Validate: alwaysValid, Policy: AbortOnRowError}
	data, err := c.Create(context.Background(), []string{"alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Skipped) != 1 || len(data.Created) != 0 {
		t.Fatalf("data = %+v", data)
	}
}

func TestCreatePolicies(t *testing.T) {
	boom := errors.New("db is on fire")

	abort := Creator{Store: &fakeStore{existsErr: boom}, Validate: alwaysValid, Policy: AbortOnRowError}
	if _, err := abort.Create(context.Background(), []string{"alice", "bob"}); !errors.Is(err, boom) {
		t.Fatalf("AbortOnRowError err = %v, want %v", err, boom)
	}

	collect := Creator{Store: &fakeStore{existsErr: boom}, Validate: alwaysValid, Policy: CollectRowErrors}
	data, err := collect.Create(context.Background(), []string{"alice", "bob"})
	if err != nil {
		t.Fatalf("CollectRowErrors err = %v, want nil", err)
	}
	if len(data.Errors) != 2 {
		t.Fatalf("errors = %v, want one per row", data.Errors)
	}
}

func TestAdminUsernameDefaultAndOverride(t *testing.T) {
	if got := AdminUsername("ADMINUSERS_TEST_ADMIN"); got != "pecheny" {
		t.Fatalf("default = %q", got)
	}
	t.Setenv("ADMINUSERS_TEST_ADMIN", " boss ")
	if got := AdminUsername("ADMINUSERS_TEST_ADMIN"); got != "boss" {
		t.Fatalf("override = %q", got)
	}
}
