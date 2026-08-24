package xycli

import (
	"strings"
	"unicode"
)

// Folding: the forgiving comparison a search matches by, so that a stress mark,
// a non-breaking space or a «ёлочка» the typography pass put there cannot hide a
// question from the editor who wrote it. The rules are web/ts/find.ts's — every
// one of them is something typo.ts writes over text an editor typed plainly.
const (
	nbsp      = ' '
	nbHyphen  = '‑'
	dashes    = "‐‑‒–—−"
	foldQuote = "«»„“”‘’"
)

// Fold normalises one string for searching: combining marks dropped, dashes and
// quotes levelled, ё→е, non-breaking space and hyphen made ordinary, case cast
// down.
func Fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Mn, r): // a stress accent the editor never typed
			continue
		case strings.ContainsRune(dashes, r), r == nbHyphen:
			b.WriteRune('-')
		case strings.ContainsRune(foldQuote, r):
			b.WriteRune('"')
		case r == 'ё' || r == 'Ё':
			b.WriteRune('е')
		case r == nbsp:
			b.WriteRune(' ')
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}
