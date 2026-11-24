package qyserver

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"code.pecheny.me/pecheny/dopesuite/dopecore/buildinfo"
	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
	"code.pecheny.me/pecheny/dopesuite/dopecore/webassets"

	"qy/qy/storage/store"
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
		Handler:           webassets.Gzip(srv.web().FinishPending(mux)),
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
	case "import-sheet":
		runImportSheet(args)
	case "deal":
		runDeal(args)
	default:
		log.Fatalf("unknown command %q (adduser, deal, import-archive, import-gq, import-sheet, version)", cmd)
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

// runDeal is the contest page's «deal» button from the shell:
//
//	qy-server deal <year>
func runDeal(args []string) {
	if len(args) != 1 {
		log.Fatal("usage: qy-server deal <year>")
	}
	year, err := strconv.Atoi(args[0])
	if err != nil {
		log.Fatal("usage: qy-server deal <year>")
	}
	s, err := newServer()
	if err != nil {
		log.Fatal(err)
	}
	defer s.db.Close()
	ctx := context.Background()
	c, err := store.Contest(ctx, s.db, year)
	if err != nil {
		log.Fatal(err)
	}
	n, err := s.deal(ctx, c)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("deal: numbered %d questions for %d", n, year)
}
