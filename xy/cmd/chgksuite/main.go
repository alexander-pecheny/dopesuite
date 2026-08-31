// Command chgksuite is the Go side of chgksuite's own CLI: the same commands,
// over the ports in internal/chgk, checked against the Python tool's output.
// Only the commands listed in usage are ported so far — see
// chgksuite_go_rewrite.md for what is still missing.
package main

import (
	"fmt"
	"os"
)

func usage() {
	fmt.Fprint(os.Stderr, `usage: chgksuite <command> [<args>]

commands:
  parse [flags] <file.docx|file.txt>…   read a package into .4s
  compose docx [flags] <file.4s>…   render questions to .docx
  compose telegram [flags] <file.4s>  post questions to a telegram channel
  compose markdown|redditmd [flags] <file.4s>…   render to markdown
  compose base [flags] <file.4s>…   render to db.chgk.info's submission text
  compose openquiz [flags] <file.4s>…   render to openquiz.me's JSON

run a command with -h for its flags
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "parse":
		err = parseCmd(os.Args[2:])
	case "compose":
		err = compose(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "chgksuite:", err)
		os.Exit(1)
	}
}
