package main

import (
	"fmt"
	"os"
	"strings"
)

// The usage screen, which is the first thing anyone sees. It is a table, and it
// is drawn as one: the verbs in a column, then what each takes, then what it
// does. Colour only on a terminal — piped, it is the same words in plain text.

func usage() {
	out := os.Stderr
	tty := isTerminal(out)

	verbWidth, argsWidth := 0, 0
	for _, c := range commands {
		verbWidth = max(verbWidth, len(c.verb))
		argsWidth = max(argsWidth, len([]rune(c.args)))
	}

	var b strings.Builder
	b.WriteString(pick(tty, styleUsageHead.Render("chgksuite")+styleNote.Render(" <command> [<args>]"),
		"usage: chgksuite <command> [<args>]"))
	b.WriteString("\n\n")
	for _, c := range commands {
		if c.verb == "" {
			b.WriteString("\n")
			continue
		}
		// Pad outside the style, so the escapes wrap the word and not the gap.
		verb, args := c.verb, c.args
		if tty {
			verb, args = styleVerb.Render(verb), styleArgs.Render(args)
		}
		fmt.Fprintf(&b, "  %s%s  %s%s  %s\n",
			verb, spaces(verbWidth-len(c.verb)),
			args, spaces(argsWidth-len([]rune(c.args))),
			c.what)
	}
	b.WriteString("\n")
	b.WriteString(pick(tty, styleNote.Render(usageTail), usageTail))
	b.WriteString("\n")
	fmt.Fprint(out, b.String())
}

const usageTail = `run a command with -h for its flags
pdf and handouts render with the typst binary: --typst, $CHGKSUITE_TYPST, or "typst" on PATH`

func pick(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

func spaces(n int) string { return strings.Repeat(" ", max(n, 0)) }
