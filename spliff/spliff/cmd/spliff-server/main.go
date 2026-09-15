// Command spliff-server is Spliff's one binary: the web server, and the login
// bot polling inside it (root ADR-0005). With no argument it serves; `adduser`
// and `version` are the two maintenance subcommands.
package main

import spliffserver "spliff/spliff/server"

func main() { spliffserver.Main() }
