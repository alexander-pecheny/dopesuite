package server

import (
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Main is the server entry point, invoked by cmd/xy-server. The `invite`
// subcommand mints a one-shot registration invite and prints it.
func Main() {
	if len(os.Args) > 1 && os.Args[1] == "invite" {
		runMintInvite(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "adduser" {
		runAddUser(os.Args[2:])
		return
	}

	srv, err := newServer()
	if err != nil {
		log.Fatal(err)
	}

	source, mode := staticSource()
	srv.assetSource = source
	srv.assetNoCache = mode == "disk"
	if !srv.assetNoCache {
		srv.assetETags = buildAssetETags(source)
	}

	mux := http.NewServeMux()

	// ---- HTML pages ----
	mux.HandleFunc("GET /", srv.handleIndex)
	mux.HandleFunc("GET /login", srv.servePage("static/login.html"))
	mux.HandleFunc("GET /register", srv.servePage("static/register.html"))
	mux.HandleFunc("GET /profile", srv.servePage("static/profile.html"))
	mux.HandleFunc("GET /profile/tokens", srv.servePage("static/tokens.html"))
	mux.HandleFunc("GET /board/{id}", srv.servePage("static/board.html"))
	mux.HandleFunc("GET /import", srv.servePage("static/import.html"))

	// ---- PWA: service worker + manifest at the site root (scope '/') ----
	mux.HandleFunc("GET /sw.js", srv.serveRootAsset(
		"static/sw.js", "text/javascript; charset=utf-8", "no-cache",
		map[string]string{"Service-Worker-Allowed": "/"}))
	mux.HandleFunc("GET /manifest.webmanifest", srv.serveRootAsset(
		"static/manifest.webmanifest", "application/manifest+json; charset=utf-8",
		"public, max-age=3600", nil))

	// ---- auth API ----
	mux.HandleFunc("POST /api/auth/register/start", srv.handleRegisterStart)
	mux.HandleFunc("GET /api/auth/register/status", srv.handleRegisterStatus)
	mux.HandleFunc("POST /api/auth/login/start", srv.handleLoginStart)
	mux.HandleFunc("POST /api/auth/login", srv.handleLoginCode)
	mux.HandleFunc("POST /api/auth/login-password", srv.handleLoginPassword)
	mux.HandleFunc("POST /api/auth/logout", srv.handleLogout)
	mux.HandleFunc("GET /api/auth/me", srv.handleMe)
	mux.HandleFunc("POST /api/auth/username", srv.handleSetUsername)
	mux.HandleFunc("POST /api/auth/password", srv.handleSetPassword)

	// ---- API tokens (Trello-compatible API credentials) ----
	mux.HandleFunc("GET /api/tokens", srv.handleListTokens)
	mux.HandleFunc("POST /api/tokens", srv.handleCreateToken)
	mux.HandleFunc("DELETE /api/tokens/{id}", srv.handleRevokeToken)

	// ---- Trello-compatible API (token-authed via key+token params) ----
	mux.HandleFunc("GET /1/boards/{id}", srv.handleTrelloGetBoard)
	mux.HandleFunc("GET /1/boards/{id}/lists", srv.handleTrelloGetLists)
	mux.HandleFunc("POST /1/lists/{id}/cards", srv.handleTrelloCreateCard)

	// ---- telegram bridge (shared-secret) ----
	mux.HandleFunc("POST /api/telegram/register", srv.handleTelegramRegister)
	mux.HandleFunc("POST /api/telegram/login", srv.handleTelegramLogin)

	// ---- boards API ----
	mux.HandleFunc("GET /api/boards", srv.handleListBoards)
	mux.HandleFunc("POST /api/boards", srv.handleCreateBoard)
	mux.HandleFunc("GET /api/boards/{id}", srv.handleGetBoard)
	mux.HandleFunc("PATCH /api/boards/{id}", srv.handlePatchBoard)
	mux.HandleFunc("DELETE /api/boards/{id}", srv.handleDeleteBoard)
	mux.HandleFunc("GET /api/boards/{id}/keymeta", srv.handleGetKeymeta)
	mux.HandleFunc("PUT /api/boards/{id}/keymeta", srv.handlePutKeymeta)
	mux.HandleFunc("GET /api/boards/{id}/player-map", srv.handleGetPlayerMap)
	mux.HandleFunc("PUT /api/boards/{id}/player-map", srv.handlePutPlayerMap)
	mux.HandleFunc("GET /api/boards/{id}/members", srv.handleListMembers)
	mux.HandleFunc("POST /api/boards/{id}/members", srv.handleAddMember)
	mux.HandleFunc("DELETE /api/boards/{id}/members/{userId}", srv.handleRemoveMember)

	// ---- lists / cards / labels / timeline ----
	mux.HandleFunc("POST /api/boards/{id}/lists", srv.handleCreateList)
	mux.HandleFunc("PATCH /api/lists/{id}", srv.handlePatchList)
	mux.HandleFunc("DELETE /api/lists/{id}", srv.handleDeleteList)
	mux.HandleFunc("POST /api/lists/{id}/cards", srv.handleCreateCard)
	mux.HandleFunc("PATCH /api/cards/{id}", srv.handlePatchCard)
	mux.HandleFunc("DELETE /api/cards/{id}", srv.handleDeleteCard)
	mux.HandleFunc("GET /api/boards/{id}/labels", srv.handleListLabels)
	mux.HandleFunc("POST /api/boards/{id}/labels", srv.handleCreateLabel)
	mux.HandleFunc("PATCH /api/labels/{id}", srv.handlePatchLabel)
	mux.HandleFunc("DELETE /api/labels/{id}", srv.handleDeleteLabel)
	mux.HandleFunc("PUT /api/cards/{id}/labels", srv.handleSetCardLabels)
	mux.HandleFunc("GET /api/cards/{id}/timeline", srv.handleGetTimeline)
	mux.HandleFunc("POST /api/cards/{id}/comments", srv.handleAddComment)

	// ---- export (chgksuite docx) ----
	mux.HandleFunc("POST /api/export/docx", srv.handleExportDocx)

	// ---- attachments ----
	mux.HandleFunc("GET /api/cards/{id}/attachments", srv.handleListAttachments)
	mux.HandleFunc("POST /api/cards/{id}/attachments", srv.handleCreateAttachment)
	mux.HandleFunc("GET /api/attachments/{id}", srv.handleGetAttachment)
	mux.HandleFunc("DELETE /api/attachments/{id}", srv.handleDeleteAttachment)

	// ---- static ----
	mux.Handle("GET /static/", staticFileServer(source, srv.assetNoCache, srv.assetETags))

	port := strings.TrimPrefix(os.Getenv("PORT"), ":")
	if port == "" {
		port = "9673"
	}
	addr := ":" + port
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("bind %s: %v", addr, err)
	}
	log.Printf("xy serving on %s (assets from %s)", addr, mode)

	httpSrv := &http.Server{
		Handler:           gzipMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Fatal(httpSrv.Serve(listener))
}

// handleIndex serves the board-list home page (client-side auth-gated).
func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.servePage("static/index.html")(w, r)
}
