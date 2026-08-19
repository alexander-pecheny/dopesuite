package server

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"pecheny.me/dopecore/buildinfo"
	"pecheny.me/dopecore/session"
	"pecheny.me/dopecore/webassets"
)

// xy's deployed environment predates the shared session package, so it keeps
// its own env-var name for the production switch.
func init() { session.ProdEnvVar = "XY_ENV" }

// Main is the server entry point, invoked by cmd/xy-server. With no argument it
// serves; the subcommands are maintenance tools that run against an existing DB.
func Main() {
	if len(os.Args) > 1 {
		switch cmd := os.Args[1]; cmd {
		case "version":
			fmt.Println(buildinfo.Version())
		case "invite":
			requireExistingDB()
			runMintInvite(os.Args[2:])
		case "adduser":
			requireExistingDB()
			runAddUser(os.Args[2:])
		case "backup":
			requireExistingDB()
			runBackup(os.Args[2:])
		case "gc":
			requireExistingDB()
			runGC()
		default:
			log.Fatalf("unknown command %q (invite, adduser, backup, gc, version)", cmd)
		}
		return
	}

	if err := checkPublicURL(session.SecureCookies(), os.Getenv("XY_PUBLIC_URL")); err != nil {
		log.Fatal(err)
	}

	srv, err := newServer()
	if err != nil {
		log.Fatal(err)
	}

	srv.assets, srv.pages = newAssets()
	if err := srv.pages.Warm(pagePaths...); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()

	// ---- HTML pages ----
	mux.HandleFunc("GET /", srv.handleIndex)
	mux.HandleFunc("GET /login", srv.servePage("ui/login.dopeui"))
	mux.HandleFunc("GET /register", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/login", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /profile", srv.servePage("ui/profile.dopeui"))
	mux.HandleFunc("GET /profile/tokens", srv.servePage("ui/tokens.dopeui"))
	mux.HandleFunc("GET /board/{id}", srv.servePage("ui/board.dopeui"))
	mux.HandleFunc("GET /import", srv.servePage("ui/import.dopeui"))

	// ---- admin tooling (gated on the configured admin username) ----
	mux.HandleFunc("GET /admin", srv.HandleAdminLanding)
	mux.HandleFunc("GET /admin/create_users", srv.HandleAdminCreateUsers)
	mux.HandleFunc("POST /admin/create_users", srv.HandleAdminCreateUsers)
	mux.HandleFunc("GET /admin/users", srv.HandleAdminUsers)

	// ---- PWA: service worker + manifest at the site root (scope '/') ----
	mux.HandleFunc("GET /sw.js", srv.serveRootAsset(
		"static/dist/sw.js", "text/javascript; charset=utf-8", "no-cache",
		map[string]string{"Service-Worker-Allowed": "/"}))
	mux.HandleFunc("GET /manifest.webmanifest", srv.serveRootAsset(
		"static/manifest.webmanifest", "application/manifest+json; charset=utf-8",
		"public, max-age=3600", nil))
	mux.HandleFunc("GET /favicon.ico", srv.serveRootAsset(
		"static/favicon.ico", "image/x-icon", "public, max-age=86400", nil))

	// ---- auth API ----
	mux.HandleFunc("GET /api/auth/methods", srv.handleLoginMethods)
	mux.HandleFunc("POST /api/auth/tg/start", srv.handleTgStart)
	mux.HandleFunc("GET /api/auth/tg/status", srv.handleTgStatus)
	mux.HandleFunc("POST /api/auth/tg/claim", srv.handleTgClaim)
	mux.HandleFunc("POST /api/auth/login-password", srv.handleLoginPassword)
	mux.HandleFunc("POST /api/auth/logout", srv.handleLogout)
	mux.HandleFunc("GET /api/auth/me", srv.handleMe)
	mux.HandleFunc("GET /api/auth/storage", srv.handleStorage)
	mux.HandleFunc("POST /api/auth/username", srv.handleSetUsername)
	mux.HandleFunc("POST /api/auth/password", srv.handleSetPassword)
	mux.HandleFunc("POST /api/auth/sizes", srv.handleSetSizes)
	mux.HandleFunc("POST /api/auth/default-author", srv.handleSetDefaultAuthor)
	mux.HandleFunc("POST /api/auth/card-title", srv.handleSetCardTitle)
	mux.HandleFunc("POST /api/auth/feed-default", srv.handleSetFeedDefault)
	mux.HandleFunc("POST /api/auth/profile-defaults", srv.handleSetProfileDefaults)
	mux.HandleFunc("POST /api/auth/announce-cities", srv.handleSetAnnounceCities)

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
	mux.HandleFunc("POST /api/boards/{id}/visit", srv.handleBoardVisit)
	mux.HandleFunc("PATCH /api/boards/{id}", srv.handlePatchBoard)
	mux.HandleFunc("POST /api/boards/{id}/migrate-name", srv.handleMigrateName)
	mux.HandleFunc("DELETE /api/boards/{id}", srv.handleDeleteBoard)
	mux.HandleFunc("GET /api/boards/{id}/keymeta", srv.handleGetKeymeta)
	mux.HandleFunc("PUT /api/boards/{id}/keymeta", srv.handlePutKeymeta)
	mux.HandleFunc("GET /api/boards/{id}/members", srv.handleListMembers)
	mux.HandleFunc("POST /api/boards/{id}/members", srv.handleAddMember)
	mux.HandleFunc("DELETE /api/boards/{id}/members/{userId}", srv.handleRemoveMember)
	mux.HandleFunc("GET /api/collaborators", srv.handleListCollaborators)

	// ---- lists / cards / labels / timeline ----
	mux.HandleFunc("POST /api/boards/{id}/lists", srv.handleCreateList)
	mux.HandleFunc("PATCH /api/lists/{id}", srv.handlePatchList)
	mux.HandleFunc("DELETE /api/lists/{id}", srv.handleDeleteList)
	mux.HandleFunc("POST /api/boards/{id}/list-groups", srv.handleCreateListGroup)
	mux.HandleFunc("PATCH /api/list-groups/{id}", srv.handlePatchListGroup)
	mux.HandleFunc("DELETE /api/list-groups/{id}", srv.handleDeleteListGroup)
	mux.HandleFunc("POST /api/lists/{id}/cards", srv.handleCreateCard)
	mux.HandleFunc("PATCH /api/cards/{id}", srv.handlePatchCard)
	mux.HandleFunc("DELETE /api/cards/{id}", srv.handleDeleteCard)
	mux.HandleFunc("GET /api/boards/{id}/labels", srv.handleListLabels)
	mux.HandleFunc("POST /api/boards/{id}/labels", srv.handleCreateLabel)
	mux.HandleFunc("GET /api/boards/{id}/sessions", srv.handleListSessions)
	mux.HandleFunc("POST /api/boards/{id}/sessions", srv.handleCreateSession)
	mux.HandleFunc("PATCH /api/sessions/{id}", srv.handlePatchSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", srv.handleDeleteSession)
	mux.HandleFunc("GET /api/sessions/{id}/timeline", srv.handleGetSessionTimeline)
	mux.HandleFunc("POST /api/sessions/{id}/comments", srv.handleAddSessionComment)
	mux.HandleFunc("PATCH /api/labels/{id}", srv.handlePatchLabel)
	mux.HandleFunc("DELETE /api/labels/{id}", srv.handleDeleteLabel)
	mux.HandleFunc("PUT /api/cards/{id}/labels", srv.handleSetCardLabels)
	mux.HandleFunc("PUT /api/cards/{id}/sessions", srv.handleSetCardSessions)
	mux.HandleFunc("PUT /api/boards/{id}/tour-testers", srv.handleSetTourTesters)
	mux.HandleFunc("GET /api/cards/{id}/timeline", srv.handleGetTimeline)
	mux.HandleFunc("GET /api/boards/{id}/comments", srv.handleGetBoardComments)
	mux.HandleFunc("POST /api/cards/{id}/comments", srv.handleAddComment)
	mux.HandleFunc("POST /api/cards/{id}/timeline/import", srv.handleImportEvents)
	mux.HandleFunc("PATCH /api/comments/{id}", srv.handlePatchComment)
	mux.HandleFunc("DELETE /api/comments/{id}", srv.handleDeleteComment)
	mux.HandleFunc("POST /api/cards/{id}/reactions", srv.handleAddReaction)
	mux.HandleFunc("DELETE /api/reactions/{id}", srv.handleDeleteReaction)

	// ---- read markers / activity (blue dots + 🔔 bell) ----
	mux.HandleFunc("POST /api/cards/{id}/read", srv.handleMarkRead)
	mux.HandleFunc("GET /api/boards/{id}/activity", srv.handleBoardActivity)
	mux.HandleFunc("POST /api/boards/{id}/read-all", srv.handleBoardReadAll)

	// ---- export (the same package as .docx or as a typst-laid-out .pdf;
	//      /pack renders several formats at once and zips them) ----
	mux.HandleFunc("POST /api/export/docx", srv.handleExportDocx)
	mux.HandleFunc("POST /api/export/pdf", srv.handleExportPDF)
	mux.HandleFunc("POST /api/export/pack", srv.handleExportPack)

	// ---- import (.4s / .zip / .docx → 4s source + images; plain text → 4s) ----
	mux.HandleFunc("POST /api/import/parse", srv.handleImportParse)
	mux.HandleFunc("POST /api/import/text", srv.handleImportText)

	// ---- Trello import proxy (read-only passthrough to the Trello API) ----
	mux.HandleFunc("POST /api/import/trello/proxy", srv.handleTrelloProxy)

	// ---- handouts ----
	mux.HandleFunc("POST /api/handouts/pdf", srv.handleHandoutsPDF)
	mux.HandleFunc("POST /api/handouts/split_fit", srv.handleHandoutsSplitFit)
	mux.HandleFunc("POST /api/handouts/stage", srv.handleHandoutsStage)
	mux.HandleFunc("POST /api/handouts/heartbeat", srv.handleHandoutsHeartbeat)
	mux.HandleFunc("DELETE /api/handouts/stage", srv.handleHandoutsUnstage)

	// ---- attachments ----
	mux.HandleFunc("GET /api/cards/{id}/attachments", srv.handleListAttachments)
	mux.HandleFunc("POST /api/cards/{id}/attachments", srv.handleCreateAttachment)
	mux.HandleFunc("GET /api/attachments/{id}", srv.handleGetAttachment)
	mux.HandleFunc("PUT /api/attachments/{id}", srv.handleReplaceAttachment)
	mux.HandleFunc("PATCH /api/attachments/{id}", srv.handlePatchAttachment)
	mux.HandleFunc("DELETE /api/attachments/{id}", srv.handleDeleteAttachment)

	// ---- static ----
	// styles.css (core+xy concat) and fonts (from the kit) win over the generic
	// file server via Go 1.22 most-specific-pattern routing.
	mux.HandleFunc("GET /static/styles.css", srv.assets.ServeStylesheet())
	mux.HandleFunc("GET /static/login.js", srv.assets.ServeShared("/static/login.js"))
	mux.HandleFunc("GET /static/menu.js", srv.assets.ServeShared("/static/menu.js"))
	mux.Handle("GET /static/fonts/", srv.assets.ServeFonts())
	mux.Handle("GET /static/", srv.assets.FileServer())

	port := strings.TrimPrefix(os.Getenv("PORT"), ":")
	if port == "" {
		port = "9673"
	}
	addr := ":" + port
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("bind %s: %v", addr, err)
	}
	// Compile typst (wasm) ahead of the first handout request: cold, that is a ~15s
	// wasm compile, which no user should sit through.
	srv.warmTypst()
	go srv.reapLoop()

	log.Printf("xy %s serving on %s (assets from %s)", buildinfo.Version(), addr, srv.assets.Mode)

	httpSrv := &http.Server{
		Handler:           webassets.Gzip(mux),
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
	s.servePage("ui/index.dopeui")(w, r)
}
