// xy-local serves localserver on a port, so local mode can be tried in a
// browser before the desktop app exists.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"code.pecheny.me/pecheny/dopesuite/dopecore/sameorigin"

	"xy/localserver"
)

const readHeaderTimeout = 5 * time.Second

func main() {
	dir := flag.String("dir", "xy-local-data", "the data directory: xy.db and blobs/")
	addr := flag.String("addr", "127.0.0.1:9674", "where to listen")
	pick := flag.String("pick", "", "a file the board's import gets every time, in place of the app's native dialog")
	typst := flag.String("typst", "", "the typst binary to render with; empty is the built-in wasm")
	flag.Parse()

	srv, err := localserver.Open(*dir, *typst)
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()
	if *pick != "" {
		srv.SetFilePicker(func() (string, error) { return *pick, nil })
	}
	log.Printf("xy local mode on http://%s (data in %s)", *addr, *dir)
	// Nothing asks who is calling, so any page in the same browser could write
	// here; the app has no port and no such neighbours.
	h := sameorigin.Middleware(func() string { return "" }, srv.Handler())
	log.Fatal((&http.Server{Addr: *addr, Handler: h, ReadHeaderTimeout: readHeaderTimeout}).ListenAndServe())
}
