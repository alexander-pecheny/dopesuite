package textparse

import (
	"regexp"
	"strconv"
	"strings"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/typo"
)

// СИ and троика are read by a different parser from ЧГК's, and by nearly the
// same one as each other: parser.py's TroikaParser is SiParser with a handful of
// overrides. Both walk the text line by line, deciding from the line's own shape
// what it is — a battle, a theme, a question's point value, a field label — and
// leaning on the document's outline, which docxread hands over as "$$HN$$"
// markers. This port keeps the two in one parser with a mode flag, because the
// overrides are interleaved: троика's line handling falls through to СИ's.

// ParseSI reads a Своя игра package. Options is taken for symmetry with Parse
// and read for nothing yet: none of ЧГК's knobs mean anything to these two, and
// the typography modes they do vary (smart quotes, smart accents) are not
// ported here or there.
func ParseSI(text string, _ Options) fsource.Doc { return newSI(false).parse(text) }

// ParseTroika reads a троика package.
func ParseTroika(text string, _ Options) fsource.Doc { return newSI(true).parse(text) }

// element is one [type, value] pair on the way to a document.
type element struct {
	typ, value string
}

type siParser struct {
	troika bool
	typo   typo.Options

	structure      []element
	currentField   string
	currentContent string
	headingFound   bool
	inThemeList    bool
	afterTheme     bool
	// lastThemeHeading is the whole heading of the most recent theme, kept so a
	// field label right after it can turn it back into a question: the document
	// styled a question as a heading.
	lastThemeHeading string
	hasLastTheme     bool

	// троика only
	lastLineBlank  bool
	sourceListMode bool
	multiforaMode  bool
}

func newSI(troika bool) *siParser {
	return &siParser{troika: troika, typo: typo.DefaultOptions()}
}

func (p *siParser) parse(text string) fsource.Doc {
	p.multiforaMode = p.troika && reTroikaMultiforaDetect.MatchString(text)
	for _, line := range splitLines(text) {
		p.handleLine(line)
	}
	p.flush()
	return p.build()
}

func (p *siParser) apply(s string) string { return typo.Typography(s, p.typo) }

func (p *siParser) push(typ, value string) {
	p.structure = append(p.structure, element{typ, value})
}

func (p *siParser) flush() {
	if p.currentField != "" && strings.TrimSpace(p.currentContent) != "" {
		p.push(p.currentField, p.apply(typo.REW(p.currentContent)))
	}
	p.currentField, p.currentContent = "", ""
	if p.troika {
		p.sourceListMode = false
	}
}

// promoteLastThemeToQuestion rewrites the theme just pushed as a question, when
// its heading opened with a point value: the document styled it as a heading and
// a field label has just proved it was a question.
func (p *siParser) promoteLastThemeToQuestion() {
	if !p.hasLastTheme {
		return
	}
	if len(p.structure) == 0 || p.structure[len(p.structure)-1].typ != "theme" {
		p.hasLastTheme = false
		return
	}
	m := reSiQuestionNum.FindStringSubmatchIndex(p.lastThemeHeading)
	if m == nil {
		p.hasLastTheme = false
		return
	}
	num := p.lastThemeHeading[m[2]:m[3]]
	n, _ := strconv.Atoi(num)
	if !siQuestionNumbers[n] {
		p.hasLastTheme = false
		return
	}
	text := strings.TrimSpace(p.lastThemeHeading[m[1]:])
	p.structure = p.structure[:len(p.structure)-1]
	p.push("number", num)
	p.push("question", p.apply(text))
	p.hasLastTheme = false
}

// ── per-line dispatch ───────────────────────────────────────────────────────

func (p *siParser) handleLine(line string) {
	if p.troika {
		p.troikaLine(line)
		return
	}
	p.siLine(line, nil)
}

// siLine is SiParser._handle_line. headingLevel is what троика already read off
// the line, so the marker is not parsed twice.
func (p *siParser) siLine(line string, headingLevel *int) {
	stripped := typo.REW(line)
	if stripped == "" {
		p.afterTheme = false
		return
	}

	if headingLevel == nil {
		if m := reStyleHeading.FindStringSubmatch(stripped); m != nil {
			text := typo.REW(m[2])
			if text == "" {
				return
			}
			// A field label is a field even when the document styled it as a
			// heading; drop the marker and read on.
			if p.isFieldLabel(text) {
				p.promoteLastThemeToQuestion()
				stripped = text
			} else {
				level, _ := strconv.Atoi(m[1])
				p.heading(level, text)
				return
			}
		}
	}

	if reSiYourThemes.MatchString(stripped) {
		p.flush()
		p.inThemeList = true
		p.push("meta", p.apply(stripped))
		return
	}

	if m := reSiTheme.FindStringSubmatch(stripped); m != nil {
		p.inThemeList = false
		p.flush()
		p.push("theme", p.apply(strings.TrimSpace(m[2])))
		p.afterTheme = true
		return
	}

	if p.inThemeList {
		if n := len(p.structure); n > 0 && p.structure[n-1].typ == "meta" {
			p.structure[n-1].value += "\n" + p.apply(stripped)
		} else {
			p.push("meta", p.apply(stripped))
		}
		return
	}

	if reSiThemeComment.MatchString(stripped) {
		p.flush()
		p.currentField = "comment"
		p.currentContent = reSiThemeComment.ReplaceAllString(stripped, "")
		return
	}

	if reSiRoundName.MatchString(stripped) {
		p.flush()
		p.push("round", p.apply(stripped))
		return
	}
	if reSiBattle.MatchString(stripped) {
		p.flush()
		p.push("battle", p.apply(stripped))
		return
	}
	if reSiBattleNumbered.MatchString(stripped) && !reSiTheme.MatchString(stripped) {
		p.flush()
		p.push("battle", p.apply(stripped))
		return
	}

	if p.dispatchLabel(stripped, reEditor, "editor", true) {
		return
	}
	if p.dispatchAuthor(stripped) {
		return
	}
	for _, spec := range []struct {
		re    *regexp.Regexp
		field string
	}{
		{reAnswer, "answer"}, {reZachet, "zachet"}, {reNezachet, "nezachet"},
		{reComment, "comment"}, {reSource, "source"},
	} {
		if p.dispatchLabel(stripped, spec.re, spec.field, false) {
			return
		}
	}

	if p.dispatchQuestionNum(stripped) {
		return
	}
	if p.dispatchQuestionNumOnly(stripped) {
		return
	}
	if p.dispatchAuthorGratitude(stripped) {
		return
	}

	switch {
	case p.currentField != "":
		p.currentContent += "\n" + stripped
	case !p.headingFound:
		p.headingFound = true
		p.push("heading", p.apply(stripped))
	default:
		p.push("meta", p.apply(stripped))
	}
}

// isFieldLabel reports whether a heading is really one of a question's fields.
func (p *siParser) isFieldLabel(text string) bool {
	for _, re := range []*regexp.Regexp{reAnswer, reZachet, reNezachet, reComment, reSource} {
		if loc := re.FindStringIndex(text); loc != nil && loc[0] == 0 {
			return true
		}
	}
	return false
}

// heading reads a line the document itself marked as a heading: a battle at the
// top level, a theme at the third, and whatever the text says regardless.
func (p *siParser) heading(level int, text string) {
	p.flush()
	p.inThemeList = false
	if reSiRoundName.MatchString(text) {
		p.push("round", p.apply(text))
		return
	}
	if reSiBattle.MatchString(text) {
		p.push("battle", p.apply(text))
		return
	}
	switch level {
	case 1:
		p.push("battle", p.apply(text))
	case 2:
		if reThemesHeader.MatchString(text) {
			p.inThemeList = true
		}
		p.push("meta", p.apply(text))
	case 3:
		p.push("theme", p.apply(reLeadingNum.ReplaceAllString(text, "")))
		p.lastThemeHeading, p.hasLastTheme = text, true
		p.afterTheme = true
	}
}

// dispatchLabel takes a line that opens with a field's label. append writes the
// field as its own element (the editor's name belongs to the package), rather
// than opening a field the following lines continue.
func (p *siParser) dispatchLabel(stripped string, re *regexp.Regexp, field string, appendElement bool) bool {
	loc := re.FindStringIndex(stripped)
	if loc == nil || loc[0] != 0 {
		return false
	}
	var sourceIsListLabel bool
	if p.troika && field == "source" {
		label := strings.ToLower(stripped[loc[0]:loc[1]])
		sourceIsListLabel = strings.Contains(label, "источники") || strings.Contains(label, "(и)")
		// A "source" whose content opens a bracket, before this question has an
		// answer, is the answer: the label is a misprint.
		content := strings.TrimSpace(stripped[loc[1]:])
		if strings.HasPrefix(content, "[") && !p.currentQuestionHasAnswer() {
			p.flush()
			p.currentField, p.currentContent = "answer", content
			p.sourceListMode = false
			return true
		}
	}

	p.flush()
	content := strings.TrimSpace(stripped[loc[1]:])
	if appendElement {
		p.push(field, p.apply(content))
	} else {
		p.currentField, p.currentContent = field, content
	}
	if p.troika {
		p.sourceListMode = field == "source" && p.currentField == "source" &&
			(sourceIsListLabel || strings.TrimSpace(p.currentContent) == "" ||
				reTroikaSourceItem.MatchString(strings.TrimSpace(p.currentContent)))
	}
	return true
}

// dispatchAuthor: an author right after a theme belongs to the theme, and an
// author anywhere else opens a field.
func (p *siParser) dispatchAuthor(stripped string) bool {
	loc := reAuthor.FindStringIndex(stripped)
	if loc == nil || loc[0] != 0 {
		return false
	}
	p.flush()
	content := strings.TrimSpace(stripped[loc[1]:])
	if p.afterTheme {
		p.push("author", p.apply(content))
	} else {
		p.currentField, p.currentContent = "author", content
	}
	return true
}

// dispatchAuthorGratitude: «Автор благодарит…» after an author's name is a note
// about the package, not more of the name.
func (p *siParser) dispatchAuthorGratitude(stripped string) bool {
	if p.currentField != "author" || !reAuthorGratitudeMeta.MatchString(stripped) {
		return false
	}
	p.flush()
	p.push("meta", p.apply(stripped))
	return true
}

func (p *siParser) dispatchQuestionNum(stripped string) bool {
	if p.troika {
		return p.troikaQuestionNum(stripped)
	}
	m := reSiQuestionNum.FindStringSubmatchIndex(stripped)
	if m == nil {
		return false
	}
	num, _ := strconv.Atoi(stripped[m[2]:m[3]])
	if siQuestionNumbers[num] {
		p.flush()
		p.push("number", strconv.Itoa(num))
		if text := strings.TrimSpace(stripped[m[1]:]); text != "" {
			p.currentField, p.currentContent = "question", text
		}
		return true
	}
	// An inline theme header, "N. Name (Author)", with an index too small to be
	// a point value. Not while reading a source list, though: an empty
	// "Источник:" followed by numbered URLs is not a theme.
	if p.currentField == "source" && strings.TrimSpace(p.currentContent) == "" {
		return false
	}
	authored := reSiThemeAuthored.FindStringSubmatch(stripped)
	if authored != nil && !siQuestionNumbers[num] && !reURLLike.MatchString(stripped) {
		p.flush()
		p.inThemeList = false
		p.push("theme", p.apply(strings.TrimSpace(authored[2])))
		p.afterTheme = true
		return true
	}
	return false
}

func (p *siParser) dispatchQuestionNumOnly(stripped string) bool {
	re, numbers := reSiQuestionNumOnly, siQuestionNumbers
	if p.troika {
		re, numbers = reTroikaQuestionNumOnly, troikaQuestionNumbers
	}
	m := re.FindStringSubmatch(stripped)
	if m == nil {
		return false
	}
	num, _ := strconv.Atoi(m[1])
	if !numbers[num] {
		return false
	}
	if p.troika && p.shouldContinueSourceList(stripped) {
		return false
	}
	p.flush()
	p.push("number", strconv.Itoa(num))
	p.currentField, p.currentContent = "question", ""
	return true
}

// ── packing ─────────────────────────────────────────────────────────────────

// build is _build_final_structure: the flat elements become questions and the
// package's own parts around them.
func (p *siParser) build() fsource.Doc {
	var doc fsource.Doc
	q := fsource.NewQuestion()
	flushQuestion := func() {
		if q.Has("question") {
			doc = append(doc, fsource.Pair{Type: "Question", Content: q})
			q = fsource.NewQuestion()
		}
	}
	for _, el := range p.structure {
		switch {
		case structuralTypes[el.typ]:
			flushQuestion()
			doc = append(doc, fsource.Pair{Type: el.typ, Content: el.value})
		case el.typ == "number":
			flushQuestion()
			q.Set("number", el.value)
		case questionLabels[el.typ]:
			switch {
			case q.Empty() && el.typ != "question" && el.typ != "number":
				// A field with no question of its own — a theme's author, say.
				doc = append(doc, fsource.Pair{Type: el.typ, Content: el.value})
			case q.Has(el.typ):
				if prev, ok := q.Get(el.typ).(string); ok {
					q.Set(el.typ, prev+"\n"+el.value)
				} else {
					q.Set(el.typ, el.value)
				}
			default:
				q.Set(el.typ, el.value)
			}
		default:
			flushQuestion()
			doc = append(doc, fsource.Pair{Type: el.typ, Content: el.value})
		}
	}
	flushQuestion()
	splitSources(doc)
	return doc
}

// splitSources turns a multi-line source into the list of references it is.
func splitSources(doc fsource.Doc) {
	for _, pair := range doc {
		q, ok := pair.Content.(*fsource.Question)
		if !ok {
			continue
		}
		src, ok := q.Get("source").(string)
		if !ok {
			continue
		}
		var lines []any
		for _, line := range strings.Split(src, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, reLeadingNum.ReplaceAllString(line, ""))
			}
		}
		if len(lines) > 1 {
			q.Set("source", lines)
		}
	}
}
