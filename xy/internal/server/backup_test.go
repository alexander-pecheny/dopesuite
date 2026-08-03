package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xy/internal/blobstore"
)

func TestBackupCopiesBothHalves(t *testing.T) {
	srv := newBackupServer(t)
	ref, _, err := srv.blobs.Put(strings.NewReader("ciphertext"))
	if err != nil {
		t.Fatalf("put blob: %v", err)
	}
	if err := srv.addUser(context.Background(), "vasya", "correct horse battery"); err != nil {
		t.Fatalf("add user: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "snap")
	if err := srv.backup(context.Background(), dest); err != nil {
		t.Fatalf("backup: %v", err)
	}

	db, err := openDB(filepath.Join(dest, "xy.db"))
	if err != nil {
		t.Fatalf("open backup db: %v", err)
	}
	defer db.Close()
	var name string
	if err := db.QueryRow(`select username from users`).Scan(&name); err != nil {
		t.Fatalf("read backup db: %v", err)
	}
	if name != "vasya" {
		t.Errorf("username = %q, want vasya", name)
	}

	blobs, err := blobstore.New(filepath.Join(dest, "blobs"))
	if err != nil {
		t.Fatalf("open backup blobs: %v", err)
	}
	f, err := blobs.Open(ref)
	if err != nil {
		t.Fatalf("open backed-up blob: %v", err)
	}
	defer f.Close()
	got, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("read backed-up blob: %v", err)
	}
	if string(got) != "ciphertext" {
		t.Errorf("blob = %q, want ciphertext", got)
	}
}

func TestBackupRefusesToOverwrite(t *testing.T) {
	srv := newBackupServer(t)
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "xy.db"), []byte("older backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := srv.backup(context.Background(), dest); err == nil {
		t.Fatal("backup overwrote an existing xy.db")
	}
}

func newBackupServer(t *testing.T) *server {
	t.Helper()
	dir := t.TempDir()
	db, err := openDB(filepath.Join(dir, "xy.db"))
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	blobs, err := blobstore.New(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatalf("blobstore: %v", err)
	}
	return &server{db: db, blobs: blobs, staging: newHandoutStaging()}
}
