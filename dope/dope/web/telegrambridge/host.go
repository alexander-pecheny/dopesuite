// Package telegrambridge answers the login conversation the Telegram bot holds:
// consume a register code, or point the user back at the site. It reaches the
// server only through the narrow Host interface (the DB and the write lock), so
// it never imports the server package; the server constructs it via
// telegrambridge.New(s) and the in-process bot calls in (server/bot.go).
package telegrambridge

import (
	"database/sql"
)

// Host is the slice of server capabilities the telegram bridge needs.
type Host interface {
	// DB returns the shared database handle.
	DB() *sql.DB
	// Lock / Unlock guard the global write mutex around the bridge's small writes.
	Lock()
	Unlock()
}

// Server binds the bridge handlers to a Host. Construct with New.
type Server struct {
	h Host
}

// New returns a bridge Server over the given Host.
func New(h Host) *Server { return &Server{h: h} }
