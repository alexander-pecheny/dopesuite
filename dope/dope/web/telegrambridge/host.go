// Package telegrambridge holds the shared-secret HTTP endpoints the co-located
// Telegram bot calls to register/login users (/api/telegram/*). It reaches the
// server only through the narrow Host interface (DB, the write lock, the bot
// secret, login-code generation and JSON response writing), so it never imports
// the server package; the server constructs it via telegrambridge.New(s) and
// dispatches in.
package telegrambridge

import (
	"database/sql"
	"net/http"
)

// Host is the slice of server capabilities the telegram bridge needs.
type Host interface {
	// DB returns the shared database handle.
	DB() *sql.DB
	// Lock / Unlock guard the global write mutex around the bridge's small writes.
	Lock()
	Unlock()
	// BotSecret returns the configured shared secret gating the bridge (empty
	// disables it).
	BotSecret() string
	// WriteJSONValue marshals value and writes it as a JSON response.
	WriteJSONValue(w http.ResponseWriter, value any)
}

// Server binds the bridge handlers to a Host. Construct with New.
type Server struct {
	h Host
}

// New returns a bridge Server over the given Host.
func New(h Host) *Server { return &Server{h: h} }
