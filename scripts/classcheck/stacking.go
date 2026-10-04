package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Two truncation and stacking mistakes that kept coming back after they were
// fixed, each now refused outright rather than reviewed for.
//
// An ellipsis. The suite's cue for text that does not fit is a fade, and for a
// name the fade plus a popover with the whole of it (createFloatingPopover).
// `text-overflow: ellipsis` hides the end of a name with no way to read it, so
// no sheet may use it.
//
// A z-index raised on hover or focus. Sheets pin a header row and a name column
// with position:sticky, and a body cell that lifts itself on :hover or
// :focus-within climbs over that header. On a phone a tap leaves the cell
// hovered, so it stays on top while the sheet scrolls under. The lift only ever
// existed to float an in-cell popover over the neighbours; the popover now lives
// on <body>, so nothing in a sheet needs to change its layer on hover.

var (
	reRule     = regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)
	reEllipsis = regexp.MustCompile(`text-overflow\s*:\s*ellipsis`)
	reZIndex   = regexp.MustCompile(`(^|;|\s)z-index\s*:`)
	reHoverSel = regexp.MustCompile(`:(hover|focus|focus-within|focus-visible)\b`)
)

type stackingProblem struct {
	selector string
	what     string
}

// stackingProblems walks the sheet rule by rule, the way layoutOnlyClasses does,
// so a declaration is never mistaken for a selector.
func stackingProblems(src string) []stackingProblem {
	var out []stackingProblem
	for _, m := range reRule.FindAllStringSubmatch(stripComments(src), -1) {
		sel := strings.Join(strings.Fields(m[1]), " ")
		body := m[2]
		if reEllipsis.MatchString(body) {
			out = append(out, stackingProblem{sel, "text-overflow: ellipsis — fade it instead (a name: fade + popover)"})
		}
		if reZIndex.MatchString(body) && reHoverSel.MatchString(sel) {
			out = append(out, stackingProblem{sel, "z-index on hover/focus — a lifted cell climbs over the sticky header; float the popover on <body> instead"})
		}
	}
	return out
}

func reportStacking(sheet, src string) int {
	problems := stackingProblems(src)
	for _, p := range problems {
		fmt.Printf("%s: %s: %s\n", sheet, p.selector, p.what)
	}
	return len(problems)
}
