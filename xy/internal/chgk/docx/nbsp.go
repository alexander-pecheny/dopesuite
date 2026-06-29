package docx

import (
	"regexp"
	"strings"
)

// Non-breaking-space gluing, ported from chgk.js replaceNoBreak/nbSegment (itself
// a port of chgksuite's replace_no_break_spaces): short prepositions/conjunctions
// glue to the following word, trailing particles/dashes to the preceding word
// (→ U+00A0), and short hyphenated words get a non-breaking hyphen (→ U+2011).
// URLs are skipped.

const (
	nbsp     = " "
	nbHyphen = "‑"
)

var nbRight = []string{"а", "без", "в", "во", "где", "для", "же", "за", "и", "или", "из", "из-за",
	"к", "как", "на", "над", "не", "ни", "но", "о", "от", "по", "под", "при", "с", "со", "то", "у", "что", "перед"}
var nbLeft = []string{"бы", "ли", "же", "—", "–"}

var (
	nbRightRes = buildNBRight()
	nbLeftRes  = buildNBLeft()
	// chgksuite re_nbh: 0–3 cyrillic letters on each side of the hyphen, so it
	// also matches digit/bare hyphens (e.g. dates "2026-01-01" → word "-").
	reHyphen = regexp.MustCompile(`(?i)(^|[^а-яё])([а-яё]{0,3}-[а-яё]{0,3})([^а-яё]|$)`)
)

type nbRule struct {
	re   *regexp.Regexp
	repl string
}

func capFirst(w string) string {
	r := []rune(w)
	if len(r) == 0 {
		return w
	}
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	return string(r)
}

func variants(w string) []string {
	c := capFirst(w)
	if c == w {
		return []string{w}
	}
	return []string{w, c}
}

func buildNBRight() []nbRule {
	var out []nbRule
	for _, w := range nbRight {
		for _, v := range variants(w) {
			out = append(out, nbRule{
				re:   regexp.MustCompile(`(^|[ \x{00a0}])` + regexp.QuoteMeta(v) + ` `),
				repl: "${1}" + v + nbsp,
			})
		}
	}
	return out
}

func buildNBLeft() []nbRule {
	var out []nbRule
	for _, w := range nbLeft {
		for _, v := range variants(w) {
			out = append(out, nbRule{
				re:   regexp.MustCompile(` ` + regexp.QuoteMeta(v) + `([ \x{00a0}]|$)`),
				repl: nbsp + v + "${1}",
			})
		}
	}
	return out
}

func nbSegment(s string) string {
	for _, r := range nbRightRes {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	for _, r := range nbLeftRes {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	// short hyphenated words (из-за, что-то…) and digit/bare hyphens: replace the
	// hyphen with U+2011. Mirrors chgksuite's search-replace-all-occurrences loop.
	for {
		m := reHyphen.FindStringSubmatchIndex(s)
		if m == nil {
			break
		}
		word := s[m[4]:m[5]]
		repl := strings.ReplaceAll(word, "-", nbHyphen)
		if repl == word {
			break
		}
		s = strings.ReplaceAll(s, word, repl)
	}
	return s
}

func replaceNoBreak(text string) string {
	spans := httpURLSpans(text)
	if len(spans) == 0 {
		return nbSegment(text)
	}
	r := []rune(text)
	var b strings.Builder
	pos := 0
	for _, sp := range spans {
		if sp[0] < pos {
			continue
		}
		b.WriteString(nbSegment(string(r[pos:sp[0]])))
		b.WriteString(string(r[sp[0]:sp[1]]))
		pos = sp[1]
	}
	b.WriteString(nbSegment(string(r[pos:])))
	return b.String()
}
