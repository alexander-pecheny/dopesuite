package qyserver

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"code.pecheny.me/pecheny/dopesuite/dopecore/buildinfo"
	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
	"code.pecheny.me/pecheny/dopesuite/dopecore/webassets"
)

const (
	readHeaderTimeout = 5 * time.Second
	idleTimeout       = 120 * time.Second
)

func init() { session.ProdEnvVar = "QY_ENV" }

// Main is the server entry point, invoked by cmd/qy-server. With no argument
// it serves; the subcommands are maintenance tools that run against an
// existing database.
func Main() {
	if len(os.Args) > 1 {
		runCommand(os.Args[1], os.Args[2:])
		return
	}
	srv := mustServer()
	mux := routes(srv)
	listener, addr := listen()

	ctx := context.Background()
	if err := srv.seedAdmins(ctx); err != nil {
		log.Fatal(err)
	}
	srv.startBot(ctx)
	go srv.runReviewPass(ctx)

	log.Printf("qy %s serving on %s (assets from %s)", buildinfo.Version(), addr, srv.assets.Mode)
	httpSrv := &http.Server{
		Handler:           webassets.Gzip(mux),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	log.Fatal(httpSrv.Serve(listener))
}

func runCommand(cmd string, args []string) {
	switch cmd {
	case "version":
		_, _ = os.Stdout.WriteString(buildinfo.Version() + "\n")
	case "adduser":
		runAddUser(args)
	case "import-archive":
		runImportArchive(args)
	case "import-gq":
		runImportGQ(args)
	default:
		log.Fatalf("unknown command %q (adduser, import-archive, import-gq, version)", cmd)
	}
}

// listen binds the port named by $PORT, or qy's default one.
func listen() (net.Listener, string) {
	port := strings.TrimPrefix(os.Getenv("PORT"), ":")
	if port == "" {
		port = "9686"
	}
	addr := ":" + port
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("bind %s: %v", addr, err)
	}
	return listener, addr
}

func mustServer() *server {
	if err := checkPublicURL(session.SecureCookies(), os.Getenv("QY_PUBLIC_URL")); err != nil {
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
	return srv
}
