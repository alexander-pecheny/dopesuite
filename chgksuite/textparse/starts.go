package textparse

import (
	"code.pecheny.me/pecheny/dopesuite/chgksuite/typo"
	"regexp"
	"strconv"
	"strings"
)

var reNumStart = regexp.MustCompile(`^([0-9]+)\.`)

// doEnumerateHack ports do_enumerate_hack: an unlabelled "N. …" line right after
// an author is only a question if an answer shows up nearby — otherwise it's a
// numbered item of some regulation block.
func (p *parser) doEnumerateHack() {
	const lookahead = 12
	blockEnd := map[string]bool{
		"editor": true, "tour": true, "tourrev": true,
		"date": true, "heading": true, "battle": true,
	}
	prevType := ""
	for i, e := range p.structure {
		if e.Type == "" && prevType == "author" && reNumStart.MatchString(e.str()) {
			hasAnswerSoon := false
			for j := i + 1; j < i+1+lookahead && j < len(p.structure); j++ {
				nt := p.structure[j].Type
				if nt == "answer" || nt == "handout" || nt == "question" {
					hasAnswerSoon = true
					break
				}
				if blockEnd[nt] {
					break
				}
			}
			if hasAnswerSoon {
				e.Type = "question"
				e.Content = strings.TrimSpace(reNumStart.ReplaceAllString(e.str(), ""))
			}
		}
		if e.Type != "" {
			prevType = e.Type
		}
	}
}

var reNumOnly = regexp.MustCompile(`^([0-9]+)\.?$`)

// processSingleNumberLines ports process_single_number_lines: some packages put
// the question number on a line of its own. In "smart" mode that's only assumed
// when no question is marked at all and the numbers are spaced plausibly.
func (p *parser) processSingleNumberLines(mode string) {
	if mode == "" {
		mode = "smart"
	}
	if mode == "off" {
		return
	}
	lines := p.numberLines()
	patch := func(l numLine) {
		p.structure[l.idx].Type = "question"
		p.structure[l.idx].Content = questionStub + strconv.Itoa(l.num) + "."
	}
	if mode == "on" {
		for _, l := range lines {
			patch(l)
		}
		return
	}
	// smart
	for _, e := range p.structure {
		if e.Type == "question" {
			return
		}
	}
	if len(lines) == 0 {
		return
	}
	frac := float64(len(p.structure)) / float64(len(lines))
	if frac < minElemsPerNumber || frac > maxElemsPerNumber {
		return
	}
	var prev *numLine
	for i := range lines {
		l := lines[i]
		if prev == nil || (l.num-prev.num <= numberLineGap && l.idx-prev.idx > 1) {
			patch(l)
			prev = &lines[i]
		}
	}
}

// numLine is an untyped element that holds nothing but a number.
type numLine struct {
	idx, num int
}

// numberLines finds the elements that are only a number.
func (p *parser) numberLines() []numLine {
	var lines []numLine
	for i, e := range p.structure {
		if e.Type != "" {
			continue
		}
		m := reNumOnly.FindStringSubmatch(strings.TrimSpace(e.str()))
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		lines = append(lines, numLine{i, n})
	}
	return lines
}

// dupletBlitzHack ports the "duplet"/"blitz" special case (chgksuite issue #23).
// Note the Python operator precedence: a lone "duplet." forces a question
// unconditionally, while "blitz." is also gated on the surrounding elements.
func (p *parser) dupletBlitzHack() {
	for i, e := range p.structure {
		words := strings.Fields(e.str())
		hasDuplet, hasBlitz := false, false
		for _, w := range words {
			if w == dupletWord {
				hasDuplet = true
			}
			if w == blitzWord {
				hasBlitz = true
			}
		}
		if hasDuplet || (hasBlitz && e.Type != "question" &&
			(i == 0 || p.structure[i-1].Type != "question")) {
			e.Type = "question"
		}
	}
}

// answerSide are the fields a comment follows in the usual order.
var answerSide = map[string]bool{"answer": true, "zachet": true, "nezachet": true}

// markLeadingComments recognises a package that puts the comment before the
// answer: there, more comments stand right before an answer than after one.
// Each comment that leads to an answer becomes a "leadcomment", which takes the
// unlabelled lines up to the answer and which group looks past to find the
// question.
func (p *parser) markLeadingComments() {
	before, after := 0, 0
	for i, e := range p.structure {
		if e.Type != "comment" {
			continue
		}
		if i+1 < len(p.structure) && p.structure[i+1].Type == "answer" {
			before++
		}
		j := i - 1
		for j >= 0 && p.structure[j].Type == "" {
			j--
		}
		if j >= 0 && answerSide[p.structure[j].Type] {
			after++
		}
	}
	if before <= after {
		return
	}
	for i, e := range p.structure {
		if e.Type == "comment" && nextLabelled(p.structure[i+1:]) == "answer" {
			e.Type = "leadcomment"
		}
	}
}

// nextLabelled is the type of the first typed element in s, or "" if none.
func nextLabelled(s []*elem) string {
	for _, e := range s {
		if e.Type != "" {
			return e.Type
		}
	}
	return ""
}

// joinBareAuthors gives an author label that stands alone on its line the
// line after it, which is where the name is.
func (p *parser) joinBareAuthors() {
	for i := 0; i+1 < len(p.structure); i++ {
		if e := p.structure[i]; e.Type == "author" && p.rx.authorOnly.MatchString(typo.REW(e.str())) {
			appendLine(e, p.structure[i+1].str())
			p.structure = append(p.structure[:i+1], p.structure[i+2:]...)
		}
	}
}

// markQuestionStarts types the lines that open a question without a question
// marker: a numbered line after an author, a handout, a number on a line of its
// own, a duplet or blitz heading. It also marks comments written before their
// answer, and joins a bare author label to the name on the next line.
func (p *parser) markQuestionStarts(singleNumberLines string) {
	p.doEnumerateHack()
	for _, e := range p.structure {
		if e.Type == "handout" {
			e.Type = "question"
		}
	}
	p.processSingleNumberLines(singleNumberLines)
	p.dupletBlitzHack()
	p.markLeadingComments()
	p.joinBareAuthors()
}
