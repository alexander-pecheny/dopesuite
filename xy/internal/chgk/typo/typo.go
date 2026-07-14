// Package typo is a Go port of chgksuite's typotools.py — the typography pass
// the parser applies to every field it extracts (quotes, dashes, stress accents,
// percent-decoding) plus the URL-aware underscore escaping the docx reader needs.
//
// Only the modes chgksuite's DefaultArgs use ("on") are implemented; the "smart"
// variants are reachable only from the command line and are treated as "off".
package typo

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ── whitespace ──────────────────────────────────────────────────────────────
//
// typotools.WHITESPACE is {space, NBSP, newline} — note it deliberately excludes
// tabs and carriage returns.

var (
	reBadWspStart = regexp.MustCompile(`^[ \x{00a0}\n]+`)
	reBadWspEnd   = regexp.MustCompile(`[ \x{00a0}\n]+$`)
	reWspNewline  = regexp.MustCompile(`[\s\x{00a0}]+\n[\s\x{00a0}]+`)
)

// REW ports remove_excessive_whitespace.
func REW(s string) string {
	s = reBadWspStart.ReplaceAllString(s, "")
	s = reBadWspEnd.ReplaceAllString(s, "")
	return reWspNewline.ReplaceAllString(s, "\n")
}

// ── URL spans ───────────────────────────────────────────────────────────────

// reURL ports typotools.re_url. The original is a mangled copy of a well-known
// "find URLs" pattern whose trailing alternations can only ever match empty, so
// it reduces to «scheme-ish prefix followed by a run of non-space, non-bracket
// characters». Python's \s is Unicode-aware, hence the explicit \p{Z}.
var reURL = regexp.MustCompile(`(?:[a-z][\w-]+:(?:/{1,3}|[a-z0-9%])|www\d{0,3}[.]|[a-z0-9.\-]+[.][a-z]{2,4}/)[^\s\p{Z}()<>]*`)

type span struct{ start, end int }

// httpURLSpans ports _iter_http_url_spans: a hand-rolled scanner that, unlike
// reURL, lets a URL contain balanced parentheses and drops one trailing ,.;
func httpURLSpans(s string) []span {
	var out []span
	for i := 0; i < len(s); {
		if !strings.HasPrefix(s[i:], "http://") && !strings.HasPrefix(s[i:], "https://") {
			_, sz := utf8.DecodeRuneInString(s[i:])
			i += sz
			continue
		}
		j := i + 1
		bracket := 0
		for j < len(s) {
			r, sz := utf8.DecodeRuneInString(s[j:])
			if unicode.IsSpace(r) || (r == ')' && bracket == 0) {
				break
			}
			if r == '(' {
				bracket++
			} else if r == ')' && bracket > 0 {
				bracket--
			}
			j += sz
		}
		end := j
		if j > i {
			if last := s[j-1]; last == ',' || last == '.' || last == ';' {
				end = j - 1
			}
		}
		out = append(out, span{i, end})
		i = j
	}
	return out
}

// urlSpans ports iter_url_spans: the http scan merged with the reURL matches.
func urlSpans(s string) []span {
	spans := httpURLSpans(s)
	for _, m := range reURL.FindAllStringIndex(s, -1) {
		spans = append(spans, span{m[0], m[1]})
	}
	kept := spans[:0]
	for _, sp := range spans {
		if sp.start < sp.end {
			kept = append(kept, sp)
		}
	}
	spans = kept
	// sort by (start, end) — Python sorts the tuples
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && (spans[j].start < spans[j-1].start ||
			(spans[j].start == spans[j-1].start && spans[j].end < spans[j-1].end)); j-- {
			spans[j], spans[j-1] = spans[j-1], spans[j]
		}
	}
	var merged []span
	for _, sp := range spans {
		if n := len(merged); n > 0 && sp.start <= merged[n-1].end {
			if sp.end > merged[n-1].end {
				merged[n-1].end = sp.end
			}
			continue
		}
		merged = append(merged, sp)
	}
	return merged
}

var reUnescapedUnderscore = regexp.MustCompile(`\\?_`)

// EscapeUnderscoresExceptURLs ports escape_underscores_except_urls: underscores
// are the 4s italic marker, so they must be backslash-escaped everywhere except
// inside URLs (where they are common and meaningful).
func EscapeUnderscoresExceptURLs(s string, skipEscaped bool) string {
	if !strings.Contains(s, "_") {
		return s
	}
	escape := func(seg string) string {
		if !skipEscaped {
			return strings.ReplaceAll(seg, "_", `\_`)
		}
		// (?<!\\)_ — only escape underscores not already escaped.
		return reUnescapedUnderscore.ReplaceAllStringFunc(seg, func(m string) string {
			if m == `\_` {
				return m
			}
			return `\_`
		})
	}
	var b strings.Builder
	last := 0
	for _, sp := range urlSpans(s) {
		b.WriteString(escape(s[last:sp.start]))
		b.WriteString(s[sp.start:sp.end])
		last = sp.end
	}
	b.WriteString(escape(s[last:]))
	return b.String()
}

// ── quotes ──────────────────────────────────────────────────────────────────

// quoteFixer ports typotools.QuoteFixer: it walks the string tracking nesting
// depth and rewrites every quote character to the Russian convention («» at odd
// levels, „“ at even ones). If the quotes don't balance it gives up and returns
// the input untouched.
type quoteFixer struct {
	src   []rune
	out   []rune
	level int
	last  map[int]rune
	marks map[int]mark
}

type mark struct {
	opening bool
	level   int
}

func isSpaceRune(r rune) bool { return r == ' ' || r == ' ' }

func (q *quoteFixer) prev(i int) (rune, bool) {
	if i == 0 {
		return 0, false
	}
	return q.out[i-1], true
}

func (q *quoteFixer) next(i int) (rune, bool) {
	if i+1 == len(q.out) {
		return 0, false
	}
	return q.out[i+1], true
}

func (q *quoteFixer) fix() string {
	for i := range q.out {
		switch q.out[i] {
		case '«', '„':
			q.level++
			q.marks[i] = mark{true, q.level}
			q.last[q.level] = q.out[i]
		case '»', '”':
			q.marks[i] = mark{false, q.level}
			q.level--
		case '"':
			p, hasPrev := q.prev(i)
			n, hasNext := q.next(i)
			openish := q.level == 0 || !hasPrev || (isSpaceRune(p) && hasNext && !isSpaceRune(n))
			if openish {
				q.level++
				q.marks[i] = mark{true, q.level}
				q.last[q.level] = '"'
			} else if q.last[q.level] == '"' {
				q.marks[i] = mark{false, q.level}
				q.level--
			} else {
				q.level++
				q.marks[i] = mark{true, q.level}
				q.last[q.level] = '"'
			}
		case '“':
			if q.last[q.level] == '„' {
				q.marks[i] = mark{false, q.level}
				q.level--
			} else {
				q.level++
				q.marks[i] = mark{true, q.level}
			}
		}
	}
	if q.level != 0 {
		return string(q.src)
	}
	for i, m := range q.marks {
		if m.opening {
			if m.level%2 != 0 {
				q.out[i] = '«'
			} else {
				q.out[i] = '„'
			}
		} else {
			if m.level%2 != 0 {
				q.out[i] = '»'
			} else {
				q.out[i] = '“'
			}
		}
	}
	return string(q.out)
}

var (
	reApostropheAfter  = regexp.MustCompile(`(\w)'`)
	reApostropheBefore = regexp.MustCompile(`'(\w)`)
)

// GetQuotesRight ports get_quotes_right.
func GetQuotesRight(s string) string {
	if strings.Contains(s, `"`) || (strings.Contains(s, "“") && !strings.Contains(s, "„")) {
		r := []rune(s)
		q := &quoteFixer{
			src:   r,
			out:   append([]rune(nil), r...),
			last:  map[int]rune{},
			marks: map[int]mark{},
		}
		s = q.fix()
	}
	s = reApostropheAfter.ReplaceAllString(s, "$1’")
	s = reApostropheBefore.ReplaceAllString(s, "‘$1")
	return s
}

// ── dashes ──────────────────────────────────────────────────────────────────

// GetDashesRight ports get_dashes_right: a run of hyphens flanked by whitespace
// becomes an em dash. RE2 has no lookaround, so the flanks are checked by hand.
func GetDashesRight(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); {
		if rs[i] == '-' && i > 0 && unicode.IsSpace(rs[i-1]) {
			j := i
			for j < len(rs) && rs[j] == '-' {
				j++
			}
			if j < len(rs) && unicode.IsSpace(rs[j]) {
				b.WriteRune('—')
				i = j
				continue
			}
			for ; i < j; i++ {
				b.WriteRune(rs[i])
			}
			continue
		}
		b.WriteRune(rs[i])
		i++
	}
	return strings.ReplaceAll(b.String(), " – ", " — ")
}

// ── accents ─────────────────────────────────────────────────────────────────

const (
	lowerRU     = "абвгдеёжзийклмнопрстуфхцчшщъыьэюя"
	upperRU     = "АБВГДЕЁЖЗИЙКЛМНОПРСТУФХЦЧШЩЪЫЬЭЮЯ"
	combAcute   = '́'
	potentialAc = "АОУЫЭЯЕЮИ"
)

var (
	badBeginnings = map[string]bool{"Мак": true, "мак": true, "О'": true, "о’": true, "О’": true, "о'": true}
	accentsToFix  = map[rune]bool{'̀': true, '́': true, '́': true}
	// latin→cyrillic homoglyphs, both cases (typotools.LETTERS_MAPPING)
	lettersMapping = map[rune]rune{
		'a': 'а', 'e': 'е', 'y': 'у', 'o': 'о', 'u': 'и',
		'A': 'А', 'E': 'Е', 'Y': 'У', 'O': 'О', 'U': 'И',
	}
	reNotRussian = regexp.MustCompile(`[^` + lowerRU + upperRU + `]+`)
)

func isCyrillic(r rune) bool {
	return strings.ContainsRune(lowerRU, unicode.ToLower(r))
}

// detectAccent ports typotools.detect_accent: in a mixed-case word an interior
// capital vowel is the chgk convention for marking stress ("мОсква"), so it is
// lowercased and given a combining acute.
func detectAccent(s string) string {
	for _, word := range reNotRussian.Split(s, -1) {
		if word == "" || strings.ToUpper(word) == word || utf8.RuneCountInString(word) <= 1 {
			continue
		}
		w := []rune(word)
		for i := 1; i < len(w); i++ {
			if !strings.ContainsRune(potentialAc, w[i]) {
				continue
			}
			if badBeginnings[string(w[:i])] {
				continue
			}
			if i != 1 && unicode.IsUpper(w[i-1]) {
				continue
			}
			if i+1 != len(w) && unicode.IsUpper(w[i+1]) {
				continue
			}
			nw := append([]rune{}, w[:i]...)
			nw = append(nw, unicode.ToLower(w[i]), combAcute)
			nw = append(nw, w[i+1:]...)
			w = nw
			i++ // skip past the combining mark we just inserted
		}
		if nw := string(w); nw != word {
			if idx := strings.Index(s, word); idx >= 0 {
				s = s[:idx] + nw + s[idx+len(word):]
			}
		}
	}
	return s
}

// cyrLatCheckChar ports cyr_lat_check_char: a Latin letter wedged between
// Cyrillic ones and carrying a combining accent is a homoglyph typo.
func cyrLatCheckChar(i int, word []rune) string {
	char := word[i]
	if isCyrillic(char) {
		return ""
	}
	leftOK := i == 0 || isCyrillic(word[i-1]) || !unicode.IsLetter(word[i-1])
	rightOK := i == len(word)-1 || isCyrillic(word[i+1]) || !unicode.IsLetter(word[i+1])
	if !leftOK || !rightOK {
		return ""
	}
	norm := []rune(nfd(char))
	if len(norm) > 1 && norm[0] != char {
		if mapped, ok := lettersMapping[norm[0]]; ok && accentsToFix[norm[1]] {
			return string(mapped) + string(combAcute) + string(norm[2:])
		}
	}
	return ""
}

// CyrLatCheckWord ports cyr_lat_check_word; it returns "" when nothing changes.
func CyrLatCheckWord(word string) string {
	w := []rune(word)
	if len(w) == 1 {
		return ""
	}
	type rep struct{ from, to string }
	var reps []rep
	for i := range w {
		if fixed := cyrLatCheckChar(i, w); fixed != "" {
			reps = append(reps, rep{string(w[i]), fixed})
		} else if isCyrillic(w[i]) && i < len(w)-1 && accentsToFix[w[i+1]] {
			reps = append(reps, rep{string(w[i]) + string(w[i+1]), string(w[i]) + string(combAcute)})
		}
	}
	if len(reps) == 0 {
		return ""
	}
	out := word
	seen := map[string]bool{}
	for _, r := range reps {
		if seen[r.from] {
			continue
		}
		seen[r.from] = true
		out = strings.ReplaceAll(out, r.from, r.to)
	}
	return out
}

func fixAccents(s string, on bool) string {
	if on {
		s = detectAccent(s)
	}
	type rep struct{ from, to string }
	var reps []rep
	seen := map[string]bool{}
	for _, word := range strings.Fields(s) {
		if seen[word] {
			continue
		}
		if fixed := CyrLatCheckWord(word); fixed != "" {
			seen[word] = true
			reps = append(reps, rep{word, fixed})
		}
	}
	for _, r := range reps {
		s = strings.ReplaceAll(s, r.from, r.to)
	}
	return s
}

// ── percent decoding ────────────────────────────────────────────────────────

var rePercent = regexp.MustCompile(`(%[0-9a-fA-F]{2})+`)

func unhex(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	default:
		return b - 'A' + 10
	}
}

// PercentDecode ports typotools.percent_decode: percent-escapes that decode to
// valid UTF-8 are turned back into text (chgk sources are full of pasted
// Wikipedia URLs). Longest runs first, so a shorter run can't clobber a longer.
func PercentDecode(s string) string {
	groups := rePercent.FindAllString(s, -1)
	// sort by length, descending (Python: sorted(..., key=len, reverse=True))
	for i := 1; i < len(groups); i++ {
		for j := i; j > 0 && len(groups[j]) > len(groups[j-1]); j-- {
			groups[j], groups[j-1] = groups[j-1], groups[j]
		}
	}
	for _, g := range groups {
		buf := make([]byte, 0, len(g)/3)
		for i := 0; i+2 < len(g); i += 3 {
			buf = append(buf, unhex(g[i+1])<<4|unhex(g[i+2]))
		}
		if !utf8.Valid(buf) {
			continue // Python raises and leaves the escape in place
		}
		s = strings.ReplaceAll(s, g, string(buf))
	}
	return s
}

// ── the pass itself ─────────────────────────────────────────────────────────

// Options mirrors the args.typography_* switches. The zero value is all-off;
// DefaultOptions is what chgksuite's DefaultArgs use.
type Options struct {
	Whitespace bool
	Quotes     bool
	Dashes     bool
	Accents    bool
	Percent    bool
}

// DefaultOptions is chgksuite's DefaultArgs: every knob "on".
func DefaultOptions() Options {
	return Options{Whitespace: true, Quotes: true, Dashes: true, Accents: true, Percent: true}
}

// Typography ports typotools.typography for the "on"/"off" modes.
func Typography(s string, o Options) string {
	if o.Whitespace {
		s = REW(s)
	}
	if o.Quotes {
		s = GetQuotesRight(s)
		s = strings.ReplaceAll(s, "'s", "’s")
	}
	if o.Dashes {
		s = GetDashesRight(s)
	}
	if o.Accents {
		s = fixAccents(s, true)
	}
	if o.Percent {
		s = PercentDecode(s)
	}
	return s
}
