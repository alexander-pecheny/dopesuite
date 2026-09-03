package textparse

import (
	"regexp"
	"strconv"
	"strings"

	"xy/internal/chgk/typo"
)

// The troika overrides: everything SI does, plus this game's own headers (a
// group stage, a battle, a "Theme N." line), the multifora variant whose
// questions are numbered "N.M.", and a source list that has to be told apart
// from the numbered questions it looks exactly like.

// troikaLine is TroikaParser._handle_line: what it does not recognise falls
// through to SI's.
func (p *siParser) troikaLine(line string) {
	stripped := typo.REW(line)
	if stripped == "" {
		p.afterTheme = false
		p.lastLineBlank = true
		return
	}

	headingLevel := 0
	if m := reStyleHeading.FindStringSubmatch(stripped); m != nil {
		headingLevel, _ = strconv.Atoi(m[1])
		text := typo.REW(m[2])
		if text == "" {
			p.lastLineBlank = false
			return
		}
		stripped = text
	}

	for _, spec := range []struct {
		re  *regexp.Regexp
		typ string
	}{
		{reTroikaSection, "section"},
		{reTroikaBattle, "battle"},
		{reTroikaTheme, "theme"},
	} {
		if !spec.re.MatchString(stripped) {
			continue
		}
		p.flush()
		value := stripped
		if spec.typ == "theme" {
			value = normalizeTroikaTheme(stripped)
		}
		p.push(spec.typ, p.apply(value))
		p.afterTheme = spec.typ == "theme"
		p.lastLineBlank = false
		return
	}

	if reTroikaPointsSection.MatchString(stripped) {
		p.flush()
		p.push("meta", p.apply(stripped))
		p.lastLineBlank = false
		return
	}

	if p.multiforaMode && p.multiforaLine(stripped) {
		p.lastLineBlank = false
		return
	}

	if reTroikaFinal.MatchString(stripped) {
		if p.currentField == "source" && strings.TrimSpace(p.currentContent) != "" {
			p.currentContent += "\n" + stripped
		} else {
			p.flush()
			p.push("meta", p.apply(stripped))
		}
		p.lastLineBlank = false
		return
	}

	if headingLevel == 1 {
		p.flush()
		p.push("theme", p.apply(normalizeTroikaTheme(stripped)))
		p.afterTheme = true
		p.lastLineBlank = false
		return
	}

	if reTroikaHostNote.MatchString(stripped) {
		if p.currentField == "question" {
			p.siLine(stripped, true)
		} else {
			p.flush()
			p.push("meta", p.apply(stripped))
		}
		p.lastLineBlank = false
		return
	}

	p.siLine(stripped, true)
	p.lastLineBlank = false
}

// multiforaLine reads the multifora variant's own headers and questions.
func (p *siParser) multiforaLine(stripped string) bool {
	if reTroikaReserveSection.MatchString(stripped) {
		p.flush()
		p.push("battle", p.apply(stripped))
		p.afterTheme = false
		return true
	}
	if reTroikaReserveTheme.MatchString(stripped) {
		p.flush()
		p.push("theme", p.apply(stripped))
		p.afterTheme = true
		return true
	}
	if m := reTroikaMultiforaQuestion.FindStringSubmatchIndex(stripped); m != nil {
		p.flush()
		p.push("number", stripped[m[2]:m[3]])
		p.currentField = "question"
		p.currentContent = strings.TrimSpace(stripped[m[1]:])
		p.afterTheme = false
		return true
	}
	if reTroikaNumberedTheme.MatchString(stripped) && !p.shouldContinueSourceList(stripped) {
		p.flush()
		p.push("theme", p.apply(normalizeTroikaTheme(stripped)))
		p.afterTheme = true
		return true
	}
	return false
}

func (p *siParser) troikaQuestionNum(stripped string) bool {
	m := reTroikaQuestionNum.FindStringSubmatchIndex(stripped)
	if m == nil {
		return false
	}
	num, _ := strconv.Atoi(stripped[m[2]:m[3]])
	if !troikaQuestionNumbers[num] || p.shouldContinueSourceList(stripped) {
		return false
	}
	p.flush()
	p.push("number", strconv.Itoa(num))
	if text := strings.TrimSpace(stripped[m[1]:]); text != "" {
		p.currentField, p.currentContent = "question", text
	}
	return true
}

// currentQuestionHasAnswer looks back for an answer belonging to the question
// being read, stopping at whatever started it.
func (p *siParser) currentQuestionHasAnswer() bool {
	if p.currentField == "answer" {
		return true
	}
	for i := len(p.structure) - 1; i >= 0; i-- {
		switch {
		case structuralTypes[p.structure[i].typ] || p.structure[i].typ == "number":
			return false
		case p.structure[i].typ == "answer":
			return true
		}
	}
	return false
}

// shouldContinueSourceList decides whether a numbered line is the next entry of
// the source list being read, or the question that ends it. A source list and a
// troika's questions are both "1." "2." "3.", so this is the whole difficulty of
// the format: a URL is an entry, a line right after another entry is an entry,
// and after a blank line only a run that keeps counting up still is.
func (p *siParser) shouldContinueSourceList(stripped string) bool {
	if p.currentField != "source" {
		return false
	}
	if strings.TrimSpace(p.currentContent) == "" {
		if !p.sourceListMode {
			return false
		}
		item := strings.TrimSpace(replaceFirst(reTroikaSourceItem, stripped, ""))
		return reURLLike.MatchString(item)
	}
	if !p.sourceListMode {
		return false
	}
	if !p.lastLineBlank {
		return true
	}
	current := reTroikaSourceItem.FindStringSubmatch(stripped)
	item := strings.TrimSpace(replaceFirst(reTroikaSourceItem, stripped, ""))
	if reURLLike.MatchString(item) {
		return true
	}
	if current != nil {
		prev := reTroikaSourceItemLine.FindAllStringSubmatch(p.currentContent, -1)
		if len(prev) > 0 {
			last := prev[len(prev)-1]
			n, _ := strconv.Atoi(current[1])
			prevN, _ := strconv.Atoi(last[1])
			if n == prevN+1 && !reURLLike.MatchString(last[2]) {
				return true
			}
		}
	}
	return false
}

// dropThemeNumberBeforePrefix removes a "Theme N." that stands in front of
// another "Theme:", keeping the second — chgksuite writes this as a lookahead,
// which RE2 has not got, so the kept part is a group here instead.
func dropThemeNumberBeforePrefix(text string) string {
	m := reTroikaThemeNumBeforeRedundant.FindStringSubmatchIndex(text)
	if m == nil {
		return text
	}
	return text[:m[0]] + text[m[2]:]
}

// normalizeTroikaTheme drops the "Theme:" a theme's text repeats after its own
// header.
func normalizeTroikaTheme(text string) string {
	text = strings.TrimSpace(dropThemeNumberBeforePrefix(text))
	return strings.TrimSpace(replaceFirst(reTroikaRedundantThemePrefix, text, ""))
}
