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

// Spliff's deployed environment names its own production switch.
func init() { session.ProdEnvVar = "SPLIFF_ENV" }

// Main is the server entry point, invoked by cmd/spliff-server. With no
// argument it serves; the subcommands are maintenance tools that run against an
// existing database.
func Main() {
	if len(os.Args) > 1 {
		switch cmd := os.Args[1]; cmd {
		case "version":
			fmt.Println(buildinfo.Version())
		case "adduser":
			runAddUser(os.Args[2:])
		default:
			log.Fatalf("unknown command %q (adduser, version)", cmd)
		}
		return
	}

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

	mux := routes(srv)

	port := strings.TrimPrefix(os.Getenv("PORT"), ":")
	if port == "" {
		port = "9674"
	}
	addr := ":" + port
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("bind %s: %v", addr, err)
	}

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
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Fatal(httpSrv.Serve(listener))
}
