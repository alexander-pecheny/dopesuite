// Command uic compiles a .dopeui file to HTML on stdout using the kit's
// core-only vocabulary (kit.DefaultApp). Apps with an overlay ship their own uic
// wrapper. Parse/validate errors go to stderr as "file:line: message" and exit 1.
package main

import (
	"fmt"
	"os"

	"pecheny.me/dopeuikit/kit"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: uic file.dopeui")
		os.Exit(1)
	}
	path := os.Args[1]
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out, err := kit.Compile(path, src)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Stdout.Write(out)
}
