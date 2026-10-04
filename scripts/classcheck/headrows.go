package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// A sheet's head rows stick below one another, and the offsets are worked out
// in one place: dope's sheet-pins.ts sheetHead, from the height each row
// declares. Two ways to bring back the bug it fixed are refused here.
//
// A head row placed by its index. `thead tr:nth-child(2) th { top: 28px }` is
// how three sheets used to stack their heads, each restating the arithmetic,
// and Hamsa had no such rule, so its themes row stuck over its rounds.
//
// A thead built anywhere else. A page that makes its own thead can give it a
// second row that sticks at the top, over the first.

var (
	reHeadRowSel = regexp.MustCompile(`thead\s+tr:(first-child|last-child|nth-child|nth-of-type|first-of-type|last-of-type)`)
	reTopDecl    = regexp.MustCompile(`(^|;|\s)top\s*:`)
	reTheadMade  = regexp.MustCompile(`createElement\(\s*["']thead["']\s*\)|\.createTHead\(`)
)

// sheetHeadOwner is the one TypeScript file that makes a dope thead.
const sheetHeadOwner = "dope/dope/web/ts/sheet-pins.ts"

func headRowProblems(src string) []stackingProblem {
	var out []stackingProblem
	for _, m := range reRule.FindAllStringSubmatch(stripComments(src), -1) {
		sel := strings.Join(strings.Fields(m[1]), " ")
		if reHeadRowSel.MatchString(sel) && reTopDecl.MatchString(m[2]) {
			out = append(out, stackingProblem{sel, "a head row's top set by its index — declare the row's height to sheetHead (sheet-pins.ts) instead"})
		}
	}
	return out
}

func reportHeadRows(sheet, src string) int {
	problems := headRowProblems(src)
	for _, p := range problems {
		fmt.Printf("%s: %s: %s\n", sheet, p.selector, p.what)
	}
	return len(problems)
}

// theadOutsideSheetHead reports a dope page that builds a thead itself.
func theadOutsideSheetHead(path, src string) int {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "dope/dope/web/ts/") || path == sheetHeadOwner {
		return 0
	}
	n := len(reTheadMade.FindAllStringIndex(src, -1))
	if n > 0 {
		fmt.Printf("%s: builds a thead itself (%d) — use sheetHead (sheet-pins.ts), which stacks its rows\n", path, n)
	}
	return n
}
