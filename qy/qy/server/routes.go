package qyserver

import (
	"net/http"

	"qy/qy/web/route"
)

// routes is the whole table: what Main serves and what the HTTP tests hit.
// Every row states the access it asks of its caller; whether somebody sits on
// a jury depends on the year, so those handlers ask for themselves.
func routes(s *server) *http.ServeMux {
	mux := http.NewServeMux()
	deps := route.Deps{Session: s.lookupSession, IsAdmin: s.isAdmin, WriteError: writeAPIError}
	api := route.New(mux, deps, route.DenyAPI)
	pageDeps := deps
	pageDeps.WriteError = s.writePageError
	pages := route.New(mux, pageDeps, route.DenyPage)

	// ---- auth ----
	pages.Handle("GET /login", route.Public, s.handleLogin)
	api.Handle("GET /api/auth/methods", route.Public, s.handleLoginMethods)
	api.Handle("POST /api/auth/tg/start", route.Public, s.handleTgStart)
	api.Handle("GET /api/auth/tg/status", route.Public, s.handleTgStatus)
	api.Handle("POST /api/auth/tg/claim", route.Public, s.handleTgClaim)
	api.Handle("POST /api/auth/login-password", route.Public, s.handleLoginPassword)
	api.Handle("GET /api/auth/me", route.LoggedIn, s.handleMe)
	api.Handle("POST /api/auth/logout", route.LoggedIn, s.handleLogout)
	pages.Handle("GET /profile", route.LoggedIn, s.handleProfilePage)
	pages.Handle("POST /profile", route.LoggedIn, s.handleProfileSave)
	pages.Handle("POST /logout", route.LoggedIn, s.handleLogoutForm)

	// ---- the contest ----
	pages.Handle("GET /nominate", route.LoggedIn, s.handleNominatePage)
	pages.Handle("POST /nominate", route.LoggedIn, s.handleNominate)
	pages.Handle("GET /vote", route.LoggedIn, s.handleVotePage)
	pages.Handle("POST /vote", route.LoggedIn, s.handleVote)
	pages.Handle("GET /final", route.LoggedIn, s.handleJuryPage)
	pages.Handle("POST /final", route.LoggedIn, s.handleJuryScores)
	pages.Handle("GET /popular", route.LoggedIn, s.handlePopularPage)
	pages.Handle("POST /popular", route.LoggedIn, s.handlePopularVote)

	// ---- admin ----
	pages.Handle("GET /admin", route.Admin, s.handleAdmin)
	pages.Handle("POST /admin/contests", route.Admin, s.handleCreateContest)
	pages.Handle("POST /admin/admins", route.Admin, s.handleAddAdmin)
	pages.Handle("POST /admin/admins/remove", route.Admin, s.handleRemoveAdmin)
	pages.Handle("GET /admin/contest/{year}", route.Admin, s.handleContestPage)
	pages.Handle("POST /admin/contest/{year}/settings", route.Admin, s.handleContestSettings)
	pages.Handle("POST /admin/contest/{year}/jurors", route.Admin, s.handleAddJuror)
	pages.Handle("POST /admin/contest/{year}/jurors/remove", route.Admin, s.handleRemoveJuror)
	pages.Handle("POST /admin/contest/{year}/deal", route.Admin, s.handleDeal)
	pages.Handle("GET /admin/contest/{year}/questions", route.Admin, s.handleQuestionList)
	pages.Handle("GET /admin/contest/{year}/stage1", route.Admin, s.handleStage1Report)
	pages.Handle("POST /admin/contest/{year}/freeze", route.Admin, s.handleFreeze)
	pages.Handle("POST /admin/contest/{year}/unfreeze", route.Admin, s.handleUnfreeze)
	pages.Handle("GET /admin/contest/{year}/final", route.Admin, s.handleFinalReport)
	pages.Handle("POST /admin/contest/{year}/publish", route.Admin, s.handlePublish)
	pages.Handle("POST /admin/contest/{year}/unpublish", route.Admin, s.handleUnpublish)
	pages.Handle("GET /admin/question/{id}", route.Admin, s.handleQuestionPage)
	pages.Handle("POST /admin/question/{id}", route.Admin, s.handleSaveQuestion)
	pages.Handle("POST /admin/question/{id}/suggestions/{sid}", route.Admin, s.handleSuggestion)
	pages.Handle("POST /admin/question/{id}/merge", route.Admin, s.handleMerge)
	pages.Handle("POST /admin/question/{id}/reparse", route.Admin, s.handleReparse)
	pages.Handle("POST /admin/question/{id}/rerun", route.Admin, s.handleRerun)
	pages.Handle("GET /admin/pages", route.Admin, s.handlePageList)
	pages.Handle("GET /admin/pages/edit", route.Admin, s.handlePageEdit)
	pages.Handle("POST /admin/pages/save", route.Admin, s.handlePageSave)
	pages.Handle("POST /admin/pages/delete", route.Admin, s.handlePageDelete)
	pages.Handle("GET /admin/pages/history", route.Admin, s.handlePageHistory)
	pages.Handle("POST /admin/pages/restore", route.Admin, s.handlePageRestore)
	pages.Handle("GET /admin/files", route.Admin, s.handleFiles)
	pages.Handle("POST /admin/files/upload", route.Admin, s.handleFileUpload)
	pages.Handle("POST /admin/files/delete", route.Admin, s.handleFileDelete)
	mux.HandleFunc("GET /admin/create_users", s.handleAdminCreateUsers)
	mux.HandleFunc("POST /admin/create_users", s.handleAdminCreateUsers)

	// ---- static ----
	mux.HandleFunc("GET /static/styles.css", s.assets.ServeStylesheet())
	mux.HandleFunc("GET /static/login.js", s.assets.ServeShared("/static/login.js"))
	mux.HandleFunc("GET /static/menu.js", s.assets.ServeShared("/static/menu.js"))
	mux.Handle("GET /static/fonts/", s.assets.ServeFonts())
	mux.Handle("GET /static/", s.assets.FileServer())

	// ---- everything else is a CMS page, a file, or a year's generated page ----
	pages.Handle("GET /", route.Public, s.handlePublic)
	return mux
}
