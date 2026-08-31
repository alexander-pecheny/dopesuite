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
  compose pptx [flags] <file.4s>…   render questions to .pptx
  compose telegram [flags] <file.4s>  post questions to a telegram channel
  compose markdown|redditmd [flags] <file.4s>…   render to markdown
  compose base [flags] <file.4s>…   render to db.chgk.info's submission text
  compose openquiz [flags] <file.4s>…   render to open-quiz.com's JSON
  compose pdf [flags] <file.4s>…   typeset questions to PDF
  compose lj [flags] <file.4s>…   render (or post) the LiveJournal HTML
  compose add_stats [flags] <file.4s>…   add a tournament's results to the questions
  handouts generate [flags] <file.4s>   pull the handouts out into a .hndt
  handouts run [flags] <file.hndt>   render a .hndt to PDF
  handouts split_fit [flags] <file.hndt>   fit each handout to a page, one PDF each
  handouts pack [flags] <folder>   merge split-fitted handouts into printer runs
  handouts create_html [flags] <1/6|1/3|1/2|1>   scaffold a hand-laid-out handout
  handouts html2img [flags] <file.html>   that HTML to a PDF and a PNG, via chromium
  board token [flags] [<service-url>]   store the credential for trello or an xy
  board download [flags] <folder>   a board into .4s (and .docx) files
  board upload [flags] <board-url> <file.4s>…   .4s files onto a board

run a command with -h for its flags

pdf and handouts render with the typst binary when there is one (--typst, or
$CHGKSUITE_TYPST, or "typst" on PATH) and with the built-in wasm otherwise
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
	case "handouts":
		err = handouts(os.Args[2:])
	case "board", "trello":
		err = boardCmd(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fail(err)
		os.Exit(1)
	}
}
