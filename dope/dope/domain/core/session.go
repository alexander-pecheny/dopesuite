package core

import (
	"net/http"
	"time"

	"pecheny.me/dopecore/authcred"

	"pecheny.me/dopecore/session"
)

// dopeSessions is authcred's session store over dope's users table, which
// carries is_system.
var dopeSessions = authcred.Sessions{
	UserColumns: "u.username, u.telegram_username, u.is_system",
	UserDest: func(u *session.User) ([]any, func()) {
		var isSystem int
		return []any{&u.Username, &u.Telegram, &isSystem}, func() { u.IsSystem = isSystem == 1 }
	},
}

// LookupSession resolves the request to a session.User: an API token when the
// request carries `Authorization: Bearer …`, the session cookie otherwise. A
// token is the user (ADR-0021) on every route but the few that call
// LookupCookieSession.
func (e *Engine) LookupSession(r *http.Request) (session.User, bool) {
	if raw := BearerToken(r); raw != "" {
		return e.lookupAPIToken(r.Context(), raw)
	}
	return e.LookupCookieSession(r)
}

// LookupCookieSession resolves the request's session cookie to a
// session.User. The second return is false when there is no valid session; an
// expired one is deleted, a live one slides at most once a minute
// (NeedsRefresh), so most authenticated requests are one SELECT.
func (e *Engine) LookupCookieSession(r *http.Request) (session.User, bool) {
	if e.DB == nil {
		return session.User{}, false
	}
	cookie, err := r.Cookie(session.CookieName)
	if err != nil || cookie.Value == "" {
		return session.User{}, false
	}
	ctx := r.Context()
	now := time.Now().UTC()
	u, state, slide := dopeSessions.Lookup(ctx, e.DB, cookie.Value, now)
	switch state {
	case authcred.NoSession:
		return session.User{}, false
	case authcred.Expired:
		_ = authcred.DeleteSession(ctx, e.DB, u.SessionID)
		return session.User{}, false
	}
	if slide {
		if err := authcred.SlideSession(ctx, e.DB, u.SessionID, now); err != nil {
			return session.User{}, false
		}
	}
	return u, true
}
