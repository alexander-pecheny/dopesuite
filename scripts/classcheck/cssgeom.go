package main

import (
	"fmt"
	"regexp"
	"strings"
)

// The Сетка geometry check. Every fest-grid rule in dope's stylesheet reads
// its lengths from the :root tokens (--grid-row, --fest-col-min, …), so a
// phone is a second set of token values, never a second set of rules. The
// two drifted once exactly there: a media block restated the column and cell
// geometry in literals, and a cap set on the desktop rule never reached the
// phone. This walks the sheet and fails on any length literal in a Сетка
// rule that is not a token declaration.

var reGridSelector = regexp.MustCompile(`\.(fest-grid|fest-columns|grid-[a-z0-9-]+)\b`)
var reLength = regexp.MustCompile(`(^|[^\w.-])[0-9]*\.?[0-9]+(px|rem|em|ch)\b`)

type geomHit struct {
	line     int
	selector string
	decl     string
}

// gridLiterals reports every declaration inside a Сетка rule that states a
// length itself. Custom properties (--x: 24px) are where lengths belong and
// pass; `0` needs no unit and is not a length literal.
func gridLiterals(src string) []geomHit {
	src = stripComments(src)
	var hits []geomHit
	var prelude strings.Builder
	var selectors []string // open blocks, innermost last
	line := 1
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == '\n' {
			line++
		}
		switch c {
		case '{':
			selectors = append(selectors, strings.TrimSpace(prelude.String()))
			prelude.Reset()
		case '}':
			if n := len(selectors); n > 0 {
				selectors = selectors[:n-1]
			}
			prelude.Reset()
		case ';':
			decl := strings.TrimSpace(prelude.String())
			prelude.Reset()
			if len(selectors) == 0 || !reGridSelector.MatchString(selectors[len(selectors)-1]) {
				continue
			}
			if strings.HasPrefix(decl, "--") || !reLength.MatchString(decl) {
				continue
			}
			hits = append(hits, geomHit{line: line, selector: selectors[len(selectors)-1], decl: decl})
		default:
			prelude.WriteByte(c)
		}
	}
	return hits
}

func reportGridLiterals(sheet, src string) int {
	hits := gridLiterals(src)
	for _, h := range hits {
		fmt.Printf("%s:%d: Сетка geometry literal — `%s` in `%s`; give it a :root token\n",
			sheet, h.line, h.decl, h.selector)
	}
	return len(hits)
}
