// Command xy-server is the xy (ЧГК Trello) server entry point — a thin wrapper
// that delegates to the server package.
package main

import "xy/internal/server"

func main() {
	server.Main()
}
