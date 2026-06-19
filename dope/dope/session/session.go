// Package session holds the authenticated-identity type shared between the auth
// machinery, the scoped API, and the HTTP handler packages. It is a pure data
// leaf (no server coupling) so any layer can name a resolved session without
// importing the server package.
package session

import "database/sql"

// User is a resolved session identity: the session row id, the user it belongs
// to, and the display fields. Username/Telegram are nullable for system or
// telegram-only accounts; IsSystem marks the built-in system actor.
type User struct {
	SessionID int64
	UserID    int64
	Username  sql.NullString
	Telegram  sql.NullString
	IsSystem  bool
}
