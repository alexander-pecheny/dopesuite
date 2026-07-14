// Package typoedit is the typography pass the card editor's «типограф» button
// runs: chgksuite's typotools (quotes → «ёлочки», hyphen runs → em dashes,
// percent-escapes → the text they encode) plus the non-breaking-space/hyphen
// gluing the exporters apply, over 4s source.
//
// Both halves already exist — typo is the typotools port, inline owns the gluing
// that the .docx and .pdf exporters must agree on — and neither is reimplemented
// here. What this package adds is the one thing they can't do themselves: apply
// them to a 4s *document* rather than to a field's value. Every line is split at
// its marker first (fsource.SplitMarker), because a pass that sees the raw source
// would read a list item's leading "-" as a stray hyphen and turn it into an em
// dash — silently destroying the list.
package typoedit

import (
	"strings"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/inline"
	"xy/internal/chgk/typo"
)

// opts: the typotools knobs the button promises. Percent-decoding turns the
// escapes in a pasted Wikipedia URL back into the words they stand for, which is
// what chgk sources are full of. Accents are the one knob left off: detect_accent
// rewrites any mixed-case word ("мОсква") whether or not stress was meant, and the
// editor has a dedicated button for the cases where it was.
var opts = typo.Options{Quotes: true, Dashes: true, Percent: true}

// Pass typographs 4s source, marker by marker. It is idempotent: the gluing
// rules match plain spaces, so text that already carries the NBSPs is left alone.
func Pass(source string) string {
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		prefix, rest := fsource.SplitMarker(line)
		if strings.TrimSpace(rest) == "" {
			continue
		}
		lines[i] = prefix + inline.ReplaceNoBreak(typo.Typography(rest, opts))
	}
	return strings.Join(lines, "\n")
}
