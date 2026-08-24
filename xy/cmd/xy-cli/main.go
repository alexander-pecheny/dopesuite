// xy-cli — xy boards from the command line, for an agent: read and write cards,
// comments and labels, search a board, export a tour. The work is in
// internal/xycli.
package main

import (
	"os"

	"xy/internal/xycli"
)

func main() {
	os.Exit(xycli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
