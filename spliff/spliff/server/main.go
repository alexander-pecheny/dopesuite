package spliffserver

import (
	"context"
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

const (
	readHeaderTimeout = 5 * time.Second
	idleTimeout       = 120 * time.Second
)

// Spliff's deployed environment names its own production switch.
func init() { session.ProdEnvVar = "SPLIFF_ENV" }

// Main is the server entry point, invoked by cmd/spliff-server. With no
// argument it serves; the subcommands are maintenance tools that run against an
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
	srv.startBot(ctx)
	srv.rates.Start(ctx)
	// The first table is fetched here rather than on somebody's first page view,
	// so a fresh instance can state a balance from the moment it answers.
	if err := srv.rates.Ensure(ctx); err != nil {
		log.Printf("rates: first fetch: %v (the app works; balances wait for a table)", err)
	}

	log.Printf("spliff %s serving on %s (assets from %s)", buildinfo.Version(), addr, srv.assets.Mode)

	httpSrv := &http.Server{
		Handler:           webassets.Gzip(mux),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	log.Fatal(httpSrv.Serve(listener))
}

// runCommand runs one maintenance subcommand.
func runCommand(cmd string, args []string) {
	switch cmd {
	case "version":
		fmt.Println(buildinfo.Version())
	case "adduser":
		runAddUser(args)
	default:
		log.Fatalf("unknown command %q (adduser, version)", cmd)
	}
}

// listen binds the port named by $PORT, or spliff's default one.
func listen() (net.Listener, string) {
	port := strings.TrimPrefix(os.Getenv("PORT"), ":")
	if port == "" {
		port = "9676"
	}
	addr := ":" + port
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("bind %s: %v", addr, err)
	}
	return listener, addr
}

// mustServer checks the environment, opens the server and warms its pages.
func mustServer() *server {
	if err := checkPublicURL(session.SecureCookies(), os.Getenv("SPLIFF_PUBLIC_URL")); err != nil {
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
