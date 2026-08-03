package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
)

// backup writes a restorable pair into dest: a checkpointed copy of the database
// and the blob tree it references. Both halves or nothing — restoring xy.db alone
// leaves every attachment a dangling ref.
func (s *server) backup(ctx context.Context, dest string) error {
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	dbPath := filepath.Join(dest, "xy.db")
	if _, err := os.Stat(dbPath); err == nil {
		return fmt.Errorf("%s already exists — back up into an empty directory", dbPath)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// VACUUM INTO snapshots a consistent database against a live WAL, so the
	// server keeps serving while this runs and the copy needs no WAL of its own.
	if _, err := s.db.ExecContext(ctx, `vacuum into ?`, dbPath); err != nil {
		return fmt.Errorf("snapshot database: %w", err)
	}
	return copyTree(s.blobs.Root(), filepath.Join(dest, "blobs"))
}

// copyTree mirrors src into dst, hardlinking where the filesystem allows it. The
// blob store is write-once, so a link is as good as a copy and costs no disk —
// but a link across filesystems fails, and there the bytes get copied.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if err := os.Link(path, target); err == nil {
			return nil
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// runBackup is the `xy-server backup <dir>` subcommand.
func runBackup(args []string) {
	if len(args) < 1 {
		log.Fatal("usage: xy-server backup <dir>")
	}
	srv, err := newServer()
	if err != nil {
		log.Fatal(err)
	}
	if err := srv.backup(context.Background(), args[0]); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("backed up to %s\n", args[0])
}
