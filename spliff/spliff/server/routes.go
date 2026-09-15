package spliffserver

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"spliff/spliff/storage/store"
	"spliff/spliff/web/route"
)

// routes is the whole table: what Main serves and what the HTTP tests hit.
// Every row states the access it asks of its caller, and the dispatcher in
// web/route resolves the session, the Group and the Owner before the handler
// runs — so no handler below checks a role, and no handler writes a status.
func routes(s *server) *http.ServeMux {
	mux := http.NewServeMux()
	deps := route.Deps{
		Session:            s.lookupSession,
		Membership:         s.membership,
		GroupOfTransaction: s.groupOfTransaction,
		WriteError:         writeError,
	}
	// Two tables over one mux: an API refusal is a status the page's fetch
	// reads, a page refusal sends a logged-out visitor to /login carrying where
	// they were going.
	api := route.New(mux, deps, route.DenyAPI)
	pages := route.New(mux, deps, route.DenyPage)

	// ---- pages ----
	pages.Handle("GET /{$}", route.LoggedIn, s.servePage("ui/index.dopeui"))
	pages.Handle("GET /login", route.Public, s.handleLogin)
	pages.Handle("GET /group/{group}", route.Member, s.servePage("ui/group.dopeui"))
	// A bill being entered has no Transaction yet, so the editor is reached
	// through the Group it will belong to.
	pages.Handle("GET /group/{group}/new", route.Member, s.servePage("ui/transaction.dopeui"))
	pages.Handle("GET /transaction/{tx}", route.Member, s.servePage("ui/transaction.dopeui"))
	// The join landing is public on purpose: an anonymous visitor sees the
	// Group's name and a log-in button whose `next` brings them back here.
	pages.Handle("GET /join/{code}", route.Public, s.servePage("ui/join.dopeui"))
	// The account page: the chrome menu's own entry, and the only way to change
	// a password or add a Telegram to an account that came in by the other door.
	pages.Handle("GET /profile", route.LoggedIn, s.servePage("ui/profile.dopeui"))

	// ---- auth ----
	api.Handle("GET /api/auth/methods", route.Public, s.handleLoginMethods)
	api.Handle("POST /api/auth/tg/start", route.Public, s.handleTgStart)
	api.Handle("GET /api/auth/tg/status", route.Public, s.handleTgStatus)
	api.Handle("POST /api/auth/tg/claim", route.Public, s.handleTgClaim)
	api.Handle("POST /api/auth/login-password", route.Public, s.handleLoginPassword)
	api.Handle("GET /api/auth/me", route.LoggedIn, s.handleMe)
	api.Handle("POST /api/auth/logout", route.LoggedIn, s.handleLogout)
	api.Handle("POST /api/auth/password", route.LoggedIn, s.handleSetPassword)
	// Linking starts with the very same mint: the bot cannot tell a code meant
	// for logging in from one meant for linking, and neither can the code. Only
	// the ending differs — Link, not Resolve, and no session out of it.
	api.Handle("POST /api/auth/tg/link/start", route.LoggedIn, s.handleTgStart)
	api.Handle("GET /api/auth/tg/link/status", route.LoggedIn, s.handleTgLinkStatus)

	// ---- groups ----
	api.Handle("GET /api/currencies", route.LoggedIn, s.handleCurrencies)
	api.Handle("GET /api/groups", route.LoggedIn, s.handleListGroups)
	api.Handle("POST /api/groups", route.LoggedIn, s.handleCreateGroup)
	api.Handle("GET /api/groups/{group}", route.Member, s.handleGetGroup)
	api.Handle("PATCH /api/groups/{group}", route.Member, s.handlePatchGroup)
	api.Handle("DELETE /api/groups/{group}", route.Owner, s.handleDeleteGroup)
	api.Handle("POST /api/groups/{group}/owner", route.Owner, s.handleHandOver)
	api.Handle("DELETE /api/groups/{group}/members/me", route.Member, s.handleLeaveGroup)
	api.Handle("DELETE /api/groups/{group}/members/{userId}", route.Owner, s.handleKickMember)

	// ---- invite links ----
	api.Handle("GET /api/groups/{group}/invites", route.Owner, s.handleListInvites)
	api.Handle("POST /api/groups/{group}/invites", route.Owner, s.handleCreateInvite)
	api.Handle("POST /api/groups/{group}/join-requests/{userId}", route.Owner, s.handleDecideJoinRequest)
	api.Handle("POST /api/invites/{id}/revoke", route.LoggedIn, s.handleRevokeInvite)
	api.Handle("DELETE /api/invites/{id}", route.LoggedIn, s.handleDeleteInvite)
	api.Handle("GET /api/invites/code/{code}", route.LoggedIn, s.handlePeekInvite)
	api.Handle("GET /api/invites/code/{code}/public", route.Public, s.handlePublicPeek)
	api.Handle("POST /api/invites/code/{code}/join", route.LoggedIn, s.handleJoinInvite)

	// ---- transactions ----
	api.Handle("POST /api/groups/{group}/transactions", route.Member, s.handleCreateTransaction)
	api.Handle("GET /api/transactions/{tx}", route.Member, s.handleGetTransaction)
	api.Handle("PUT /api/transactions/{tx}", route.Member, s.handleUpdateTransaction)
	api.Handle("DELETE /api/transactions/{tx}", route.Member, s.handleDeleteTransaction)
	api.Handle("POST /api/transactions/{tx}/restore", route.Member, s.handleRestoreTransaction)

	// ---- photos ----
	api.Handle("POST /api/transactions/{tx}/photos", route.Member, s.handleUploadPhoto)
	api.Handle("GET /api/photos/{id}", route.LoggedIn, s.handleGetPhoto)
	api.Handle("DELETE /api/photos/{id}", route.LoggedIn, s.handleDeletePhoto)

	// ---- admin ----
	mux.HandleFunc("GET /admin", s.handleAdminLanding)
	mux.HandleFunc("GET /admin/create_users", s.handleAdminCreateUsers)
	mux.HandleFunc("POST /admin/create_users", s.handleAdminCreateUsers)

	// ---- static ----
	// styles.css (core + the Spliff layer) and the fonts win over the generic
	// file server through Go 1.22's most-specific-pattern routing.
	mux.HandleFunc("GET /static/styles.css", s.assets.ServeStylesheet())
	mux.HandleFunc("GET /static/login.js", s.assets.ServeShared("/static/login.js"))
	mux.HandleFunc("GET /static/menu.js", s.assets.ServeShared("/static/menu.js"))
	mux.Handle("GET /static/fonts/", s.assets.ServeFonts())
	mux.Handle("GET /static/", s.assets.FileServer())
	return mux
}

// membership is what the dispatcher asks about a Group: is this person in it,
// and do they own it. A Group that does not exist answers no to both, so a
// guessed id is a 404 rather than a 500.
func (s *server) membership(ctx context.Context, groupID, userID int64) (bool, bool, error) {
	var ownerID int64
	err := s.db.QueryRowContext(ctx, `select owner_id from groups where id = ?`, groupID).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	member, err := store.IsMember(ctx, s.db, groupID, userID)
	return member, member && ownerID == userID, err
}
