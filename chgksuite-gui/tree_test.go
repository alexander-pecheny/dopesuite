package main

import (
	"slices"
	"testing"
)

// walk is what fyne's tree does with the four functions it is given: start at
// the root, and draw a row for every node it reaches. Nothing here is drawn if
// the root calls itself a leaf, which is the bug this guards.
func walk(c *commands, uid string, depth int, rows *[]string) {
	if !c.isBranch(uid) {
		*rows = append(*rows, c.label(uid, false))
		return
	}
	if uid != "" {
		*rows = append(*rows, c.label(uid, true))
	}
	for _, child := range c.childUIDs(uid) {
		walk(c, child, depth+1, rows)
	}
}

var listing = []commandSpec{
	{Verb: "parse"}, {Verb: "compose docx"}, {Verb: "compose pdf"},
	{Verb: "handouts run"}, {Verb: "handouts create_html"}, {Verb: "board token"},
}

func TestTheTreeDrawsEveryCommand(t *testing.T) {
	var rows []string
	walk(group(listing), "", 0, &rows)
	want := []string{"parse", "compose", "docx", "pdf", "handouts", "run", "create_html", "board", "token"}
	if !slices.Equal(rows, want) {
		t.Errorf("tree drew %q, want %q", rows, want)
	}
}

func TestEveryCommandIsReachableByItsVerb(t *testing.T) {
	c := group(listing)
	for _, spec := range listing {
		if _, ok := c.byVerb[spec.Verb]; !ok {
			t.Errorf("%q cannot be selected", spec.Verb)
		}
	}
}
