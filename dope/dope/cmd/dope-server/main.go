// Command dope-server is the dope tournament server entry point. It is a thin
// wrapper that delegates to the dopeserver package (the HTTP server, DB, SSE
// and handlers), matching the cmd/telegram-bot layout.
package main

import dopeserver "dope/dope"

func main() {
	dopeserver.Main()
}
