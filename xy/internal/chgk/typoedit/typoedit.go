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
//
// NO PRODUCTION CALLER: the button runs the TypeScript port (web/ts/typo.ts),
// because question text must not be posted to a server that is not allowed to
// see it. This package is the parity ORACLE, and testdata/pass_cases.json is
// read by its tests AND by jstest/typo.test.js.
package typoedit

import (
	"strings"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/inline"
	"xy/internal/chgk/typo"
)

// opts: the typotools knobs the button promises. Percent-decoding turns the
// escapes in a pasted Wikipedia URL back into the words they stand for, which is
// what chgk sources are full of.
var opts = typo.Options{Quotes: typo.On, Dashes: true, Percent: true}

// accentOpts adds detect_accent: chgk marks stress by capitalising the vowel
// («брАзер»), and this turns that into a real combining acute («бра́зер»). It is a
// heuristic on capitalisation, so it is its own mode rather than part of the
// default — see PassAccents.
var accentOpts = typo.Options{Quotes: typo.On, Dashes: true, Percent: true, Accents: typo.On}

// Pass typographs 4s source, marker by marker. It is idempotent: the gluing
// rules match plain spaces, so text that already carries the NBSPs is left alone.
func Pass(source string) string { return pass(source, opts) }

// PassAccents is Pass plus stress-mark detection.
func PassAccents(source string) string { return pass(source, accentOpts) }

func pass(source string, o typo.Options) string {
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		prefix, rest := fsource.SplitMarker(line)
		if strings.TrimSpace(rest) == "" {
			continue
		}
		lines[i] = prefix + inline.ReplaceNoBreak(typo.Typography(rest, o), inline.NoBreak{})
	}
	return strings.Join(lines, "\n")
}
