package textparse

import (
	"regexp"
	"strconv"
	"strings"
	"xy/internal/chgk/typo"
)

func (p *parser) stripLabels() {
	for idx := 0; idx < len(p.structure); idx++ {
		e := p.structure[idx]
		if e.Type == "" {
			e.Type = "meta"
		}
		re := p.rx.field(e.Type)
		known := re != nil
		if !known || e.Type == "tour" || e.Type == "tourrev" || e.Type == "editor" || e.Type == "battle" {
			continue
		}
		beforeReplacement := ""
		if e.Type == "question" {
			num, hasNum := p.questionNumber(e.str())
			if hasNum {
				p.insert(idx, &elem{"number", num})
				idx++
			}
			if !hasNum {
				low := strings.ToLower(e.str())
				if strings.Contains(low, "нулевой вопрос") || strings.Contains(low, "разминочный вопрос") {
					p.insert(idx, &elem{"number", "0"})
					idx++
				}
			}
			lines := strings.Split(e.str(), sep)
			var out []string
			for _, line := range lines {
				if p.rx.field("question").MatchString(line) {
					line = replaceFirst(p.rx.field("question"), line, "")
				}
				if s := strings.TrimSpace(line); s != "" {
					out = append(out, s)
				}
			}
			e.Content = strings.Join(out, sep)
		} else {
			beforeReplacement = e.str()
			e.Content = replaceFirst(re, e.str(), "")
		}
		if strings.HasPrefix(e.str(), sep) {
			e.Content = strings.TrimPrefix(e.str(), sep)
		}
		// The gendered feminine author label is preserved as a 4s field-label override.
		if e.Type == "author" && beforeReplacement != "" &&
			strings.Contains(strings.ToLower(beforeReplacement), "авторка:") {
			e.Content = "!!Авторка" + e.str()
		}
	}
}

// questionNumber returns the (?P<number>…) group of the question regex, and
// whether it matched and was non-empty.
func (p *parser) questionNumber(s string) (string, bool) {
	m := p.rx.field("question").FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	n := m[p.rx.field("question").SubexpIndex("number")]
	return n, n != ""
}

func replaceFirst(re *regexp.Regexp, s, with string) string {
	loc := re.FindStringIndex(s)
	if loc == nil {
		return s
	}
	return s[:loc[0]] + with + s[loc[1]:]
}

func (p *parser) prettify() {
	for id := 0; id < len(p.structure); id++ {
		e := p.structure[id]

		p.detectInnerList(e)

		if e.Type == "source" {
			if s, ok := e.Content.(string); ok && len(splitLines(s)) > 1 {
				var items []any
				for _, x := range splitLines(s) {
					items = append(items, replaceFirst(p.rx.field("number"), typo.REW(x), ""))
				}
				e.Content = items
			}
		}

		if e.Type != "date" {
			e.Content = recursiveTypography(e.Content, p.opts)
		}
	}
}

var reCRLF = regexp.MustCompile(`\r?\n`)

func splitLines(s string) []string { return reCRLF.Split(s, -1) }

// reListItem is the inner-list marker "N. " / "N) ". The original also has a
// trailing `\s*(?!\d)`, which RE2 can't express; listItemEnd below does that part.
var reListItem = regexp.MustCompile(`([\s\x{00a0}]+|^)(\d+)[\.\)]`)

// listItemEnd resolves the `\s*(?!\d)` tail. Because \s* is greedy and backtracks,
// the lookahead only actually rejects a marker when NO whitespace follows it and
// the next character is a digit ("1.2" is not a list item, "1. 2" is — with the
// match then ending before the digit). It returns the end offset, or -1 to reject.
func listItemEnd(s string, end int) int {
	maxWS := end
	for maxWS < len(s) && isPySpace(rune(s[maxWS])) {
		maxWS++
	}
	for e := maxWS; e >= end; e-- {
		if e >= len(s) || s[e] < '0' || s[e] > '9' {
			return e
		}
	}
	return -1
}

// detectInnerList ports the "detect inner lists" block: a run of 1., 2., 3. …
// markers turns the element's content into a [preamble, items] list. For a
// question this only applies to duplets and blitzes (whose parts really are a list);
// elsewhere (sources, comments) any ascending run counts.
func (p *parser) detectInnerList(e *elem) {
	s, ok := e.Content.(string)
	if !ok {
		return
	}
	type match struct {
		text     string
		num      int
		start    int
		endAfter int
	}
	var all []match
	for _, loc := range reListItem.FindAllStringSubmatchIndex(s, -1) {
		numStr := s[loc[4]:loc[5]]
		n, err := strconv.Atoi(numStr)
		if err != nil {
			continue
		}
		wsEnd := listItemEnd(s, loc[1])
		if wsEnd < 0 {
			continue
		}
		all = append(all, match{s[loc[0]:wsEnd], n, loc[0], wsEnd})
	}
	if len(all) <= 1 {
		return
	}
	sorted := append([]match(nil), all...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].num < sorted[j-1].num; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	var candidate []match
	for j := 0; j < len(sorted) && j == sorted[j].num-1; j++ {
		candidate = append(candidate, sorted[j])
	}
	if len(candidate) <= 1 {
		return
	}
	for i := 1; i < len(candidate); i++ {
		if candidate[i].start <= candidate[i-1].start {
			return // false positive: a duplicate number matched at the wrong place
		}
	}
	isQuestion := e.Type == "question"
	low := strings.ToLower(s)
	// Python: element[0] != "question" or (element[0]=="question" and "duplet" in …) or "blitz" in …
	if !(!isQuestion || (isQuestion && strings.Contains(low, "дуплет")) || strings.Contains(low, "блиц")) {
		return
	}
	positions := make([]int, len(candidate))
	for i, c := range candidate {
		positions[i] = c.start
	}
	parts := partition(s, positions)
	for i, c := range candidate {
		parts[i+1] = strings.Replace(parts[i+1], c.text, "", 1)
	}
	items := make([]any, 0, len(parts)-1)
	for _, x := range parts[1:] {
		items = append(items, x)
	}
	if parts[0] != "" {
		e.Content = []any{parts[0], items}
	} else {
		e.Content = items
	}
}

// isPySpace matches Python's \s (Unicode-aware, so NBSP counts).
func isPySpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f', ' ':
		return true
	}
	return false
}

func partition(s string, indices []int) []string {
	out := make([]string, 0, len(indices)+1)
	prev := 0
	for _, i := range indices {
		out = append(out, s[prev:i])
		prev = i
	}
	return append(out, s[prev:])
}

func recursiveTypography(v any, o typo.Options) any {
	switch t := v.(type) {
	case string:
		return typo.Typography(t, o)
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = recursiveTypography(x, o)
		}
		return out
	}
	return v
}

// cleanFields drops the empty lines, strips each field's label, and pulls out
// question numbers, inner lists and typography. It reports false when nothing
// is left.
func (p *parser) cleanFields() bool {
	kept := p.structure[:0]
	for _, e := range p.structure {
		if e.Type != "" || typo.REW(e.str()) != "" {
			kept = append(kept, e)
		}
	}
	p.structure = kept
	if len(p.structure) == 0 {
		return false
	}
	if first := p.structure[0]; first.Type == "" && len(p.structure) > 1 &&
		p.rx.field("number").MatchString(typo.REW(first.str())) {
		p.structure = p.structure[1:]
		p.structure[0].Content = first.str() + sep + p.structure[0].str()
	}
	p.stripLabels()
	p.prettify()
	return true
}
