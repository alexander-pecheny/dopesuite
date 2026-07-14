// Package blobstore stores encrypted attachment bytes on disk. It is content-
// agnostic: the bytes it receives are already an xy encryption envelope, so the
// store never sees plaintext. Files are named by a random ref under a sharded
// directory tree.
package blobstore

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
)

type Store struct {
	root string
}

// New opens (creating if needed) a blob store rooted at dir.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{root: dir}, nil
}

func newRef() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Store) path(ref string) string {
	// shard by first 2 chars to avoid huge flat dirs
	shard := "00"
	if len(ref) >= 2 {
		shard = ref[:2]
	}
	return filepath.Join(s.root, shard, ref)
}

// Put writes the ciphertext from r and returns its ref.
func (s *Store) Put(r io.Reader) (string, int64, error) {
	ref, err := newRef()
	if err != nil {
		return "", 0, err
	}
	p := s.path(ref)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", 0, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	n, err := io.Copy(f, r)
	if err != nil {
		_ = os.Remove(p)
		return "", 0, err
	}
	return ref, n, nil
}

// Open returns a reader over the ciphertext for ref.
func (s *Store) Open(ref string) (*os.File, error) {
	return os.Open(s.path(ref))
}

// Remove deletes the blob for ref (best-effort).
func (s *Store) Remove(ref string) error {
	err := os.Remove(s.path(ref))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
