package listexport

import (
	"regexp"
	"strconv"
	"strings"

	xystrings "xy/i18nstrings"

	"xy/internal/cardkind"
	"xy/internal/chgk/inline"
)

// The .hndt a List's handouts are laid out from (hndt.ts, the browser's port of
// chgksuite's 4s2hndt over Cards instead of a file).
//
// The rule for a Handout written without its bracket is xy's, not chgksuite's.
// chgksuite's `handouts generate` (internal/chgk/handout.Generate, kept byte for
// byte) treats a question that mentions a handout anywhere in its text as one
// with a badly marked handout, prints a warning, and offers the whole question
// text. That is a guess for a person at a terminal who reads the warning. Here
// the same answer lights the Card's badge on the board and offers the handouts
// panel, with nobody to warn, so the label has to open the text, as a parsed
// .docx writes it, before the text counts as a handout.

const (
	// blockSep is chgksuite's .hndt block delimiter.
	blockSep    = "\n---\n"
	defaultMeta = "columns: 3"
)

type handout struct {
	image      bool
	name, text string
}

func postprocessHandout(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(inline.DropHidden(s), `\_`, "_"))
}

// imgIn is the first picture an (img …) directive in s names, hidden comments
// skipped.
func imgIn(s string) (string, bool) {
	for _, r := range inline.Parse4sElem(s) {
		if r.Kind != "img" {
			continue
		}
		if im, ok := inline.ParseImg(r.Text); ok {
			return im.Name, true
		}
	}
	return "", false
}

func handoutFrom(text string) handout {
	if name, ok := imgIn(text); ok {
		return handout{image: true, name: name}
	}
	return handout{text: postprocessHandout(text)}
}

// handoutOf is a question Card's Handout: a legacy "> " block, else the
// handout bracket in its question (the "[<label>: …]" chgksuite reads), else
// one written without the bracket.
func handoutOf(desc string) (handout, bool) {
	for _, b := range parseBlocks(desc) {
		if b.kind == "handout" {
			return handoutFrom(b.text), true
		}
	}
	q := questionText(desc)
	for _, sp := range inline.BracketSpans(q) {
		if !inline.IsHandoutBody(sp.Body) {
			continue
		}
		text := sp.Body
		if _, after, ok := strings.Cut(sp.Body, ":"); ok {
			text = strings.TrimSpace(after)
		}
		return handoutFrom(text), true
	}
	return unbracketedHandout(q)
}

var handoutLabel = regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(xystrings.Default.Chgk.Label.Handout()) + `[.:]?\s*`)

// unbracketedHandout is the Handout of a question that carries a picture, or
// opens with the handout label, without the bracket. A picture is the handout;
// otherwise the label goes and the rest of the text is offered for the author
// to cut down in the .hndt.
func unbracketedHandout(q string) (handout, bool) {
	if name, ok := imgIn(q); ok {
		return handout{image: true, name: name}, true
	}
	if !handoutLabel.MatchString(q) {
		return handout{}, false
	}
	text := postprocessHandout(handoutLabel.ReplaceAllString(q, ""))
	return handout{text: text}, text != ""
}

// hndt is the tour's preamble, then one block per question Card with a
// Handout, each under its saved settings or the default.
func hndt(cards []Card) string {
	numbers := numberCards(cards)
	var blocks []string
	for _, c := range cards {
		if c.Kind == cardkind.HandoutsPreamble {
			if p := strings.TrimSpace(c.Desc); p != "" {
				blocks = append(blocks, p)
			}
			break
		}
	}
	for i, c := range cards {
		if !cardkind.Of(c.Kind).Handouts {
			continue
		}
		// Version 1's handout only: a block per Version would print two
		// handouts under one number, and split-fit names its output by it.
		h, ok := handoutOf(c.Desc)
		if !ok {
			continue
		}
		meta := strings.TrimSpace(c.HandoutMeta)
		if meta == "" {
			meta = defaultMeta
		}
		content := h.text
		if h.image {
			content = "image: " + h.name
		}
		blocks = append(blocks, "for_question: "+numbers[i]+"\n"+meta+"\n\n"+content)
	}
	return strings.Join(blocks, blockSep)
}

// numberCards is each Card's display number, "" for a Card without one
// (chgk.ts numberQuestionCards). Questions count 1, 2, 3…; a `№ N` sets an
// explicit number and a `№№ N` resets the running base, as in chgksuite. A
// heading or meta Card has no number, but its `№№ N` resets the base for the
// questions after it. Themes run a count of their own, which a heading restarts.
func numberCards(cards []Card) []string {
	next, nextTheme := 1, 1
	out := make([]string, len(cards))
	for i, c := range cards {
		k := cardkind.Of(c.Kind)
		switch k.Counter {
		case cardkind.CounterTheme:
			out[i] = strconv.Itoa(nextTheme)
			nextTheme++
			continue
		case cardkind.CounterQuestion:
			out[i], next = questionNumber(c.Desc, next)
			continue
		}
		if k.Section {
			nextTheme = 1
		}
		if k.SetsBase {
			next = baseReset(c.Desc, next)
		}
	}
	return out
}

// numberDirective is a question's first `№` or `№№`.
func numberDirective(desc string) (value string, base, ok bool) {
	for _, b := range parseBlocks(desc) {
		switch b.kind {
		case "setcounter":
			return b.text, true, true
		case "number":
			return b.text, false, true
		}
	}
	return "", false, false
}

func questionNumber(desc string, next int) (string, int) {
	value, base, ok := numberDirective(desc)
	if !ok || value == "" {
		return strconv.Itoa(next), next + 1
	}
	n, isNum := leadingInt(value)
	if base {
		if !isNum {
			return value, next
		}
		return strconv.Itoa(n), n + 1
	}
	if isZeroNumber(value) {
		return value, next
	}
	return value, n + 1
}

func baseReset(desc string, next int) int {
	value, base, ok := numberDirective(desc)
	if !ok || !base || value == "" {
		return next
	}
	if n, isNum := leadingInt(value); isNum {
		return n
	}
	return next
}

var allDigits = regexp.MustCompile(`^\d+$`)

// isZeroNumber is chgksuite's is_zero: a number that starts with "0" or is not
// an integer, such as a warm-up's, is shown as written and does not advance
// the count.
func isZeroNumber(value string) bool {
	s := strings.TrimSpace(value)
	return strings.HasPrefix(s, "0") || !allDigits.MatchString(s)
}

var leadingDigits = regexp.MustCompile(`^\s*([+-]?\d+)`)

// leadingInt reads the integer a value opens with, as the browser's parseInt
// does: "12a" is 12, and "a12" is none.
func leadingInt(value string) (int, bool) {
	m := leadingDigits.FindStringSubmatch(value)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}
