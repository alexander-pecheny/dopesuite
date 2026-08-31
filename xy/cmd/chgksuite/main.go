// Command chgksuite is the Go side of chgksuite's own CLI: the same commands,
// over the ports in internal/chgk, checked against the Python tool's output.
// See chgksuite_go_rewrite.md for what each one does and does not do.
package main

import (
	"os"
)

// command is one line of the usage: what to type, what it takes, what it does.
type command struct {
	verb, args, what string
}

// commands is the usage, and the order it is printed in — the same commands
// chgksuite has, grouped as it groups them.
var commands = []command{
	{"parse", "<file.docx|file.txt>…", "read a package into .4s"},
	{},
	{"compose docx", "<file.4s>…", "render questions to .docx"},
	{"compose pdf", "<file.4s>…", "typeset questions to PDF"},
	{"compose pptx", "<file.4s>…", "render questions to .pptx"},
	{"compose telegram", "<file.4s>", "post questions to a telegram channel"},
	{"compose markdown|redditmd", "<file.4s>…", "render to markdown"},
	{"compose base", "<file.4s>…", "render to db.chgk.info's submission text"},
	{"compose openquiz", "<file.4s>…", "render to open-quiz.com's JSON"},
	{"compose lj", "<file.4s>…", "render (or post) the LiveJournal HTML"},
	{"compose add_stats", "<file.4s>…", "add a tournament's results to the questions"},
	{},
	{"handouts generate", "<file.4s>", "pull the handouts out into a .hndt"},
	{"handouts run", "<file.hndt>", "render a .hndt to PDF"},
	{"handouts split_fit", "<file.hndt>", "fit each handout to a page, one PDF each"},
	{"handouts pack", "<folder>", "merge split-fitted handouts into printer runs"},
	{"handouts create_html", "<1/6|1/3|1/2|1>", "scaffold a hand-laid-out handout"},
	{"handouts html2img", "<file.html>", "that HTML to a PDF and a PNG, via chromium"},
	{},
	{"board token", "[<service-url>]", "store the credential for trello or an xy"},
	{"board download", "<folder>", "a board into .4s (and .docx) files"},
	{"board upload", "<board-url> <file.4s>…", ".4s files onto a board"},
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
