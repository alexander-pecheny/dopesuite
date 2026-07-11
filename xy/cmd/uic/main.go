// Command uic compiles a .xui file to HTML on stdout. Parse/validate errors
// go to stderr as "file:line: message" and exit the process with status 1.
package main

import (
	"fmt"
	"os"

	"xy/internal/ui"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: uic file.xui")
		os.Exit(1)
	}
	path := os.Args[1]
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out, err := ui.Compile(path, src)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Stdout.Write(out)
}
