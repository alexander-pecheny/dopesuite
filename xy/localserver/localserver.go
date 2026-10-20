// Package localserver is xy in local mode, for the desktop app to serve inside
// its own process: one user and no login, boards stored as plaintext, and no
// feature that needs a second person. It opens no port; the app hands Handler
// to its webview's asset server.
package localserver

import (
	"net/http"
	"os"
	"path/filepath"

	"xy/internal/server"
)

// A data directory is the user's own, so nobody else on the computer reads it.
const dirMode = 0o700

// Server is an open local xy.
type Server struct{ s *server.Server }

// Open opens (creating on first use) the local xy whose database and attachment
// blobs live in dir. typst is the typst binary to render with; empty is the
// wasm, which a build with -tags nowasmtypst does not have.
func Open(dir, typst string) (*Server, error) {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, err
	}
	s, err := server.Open(server.Config{
		DB:    filepath.Join(dir, "xy.db"),
		Blobs: filepath.Join(dir, "blobs"),
		Local: true,
		Typst: typst,
	})
	if err != nil {
		return nil, err
	}
	s.WarmTypst()
	return &Server{s: s}, nil
}

// SetFilePicker gives the board's import the app's native file dialog, which
// returns the chosen path or "" when cancelled. Call it before serving.
func (s *Server) SetFilePicker(pick func() (string, error)) { s.s.SetFilePicker(pick) }

// Handler serves xy's pages and API.
func (s *Server) Handler() http.Handler { return s.s.Handler() }

// Close stops the background work and closes the database.
func (s *Server) Close() error { return s.s.Close() }
