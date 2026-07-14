package fsource

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// composeMarkers is compose_4s's types_mapping: the 4s prefix each element type
// is written with. It is the inverse of markerMapping in parse.go, except that
// several types share a prefix on the way out ("## " for section/tour/tourrev).
var composeMarkers = map[string]string{
	"meta": "# ", "section": "## ", "tour": "## ", "tourrev": "## ",
	"battle": "#B ", "round": "#R ", "theme": "#T ", "editor": "#EDITOR ",
	"heading": "### ", "ljheading": "###LJ ", "date": "#DATE ",
	"question": "? ", "answer": "! ", "zachet": "= ", "nezachet": "!= ",
	"source": "^ ", "comment": "/ ", "author": "@ ", "handout": "> ",
}

// composeOrder is common.QUESTION_LABELS — the order a question's fields are
// written in, regardless of the order they were parsed in.
var composeOrder = []string{
	"handout", "question", "answer", "zachet", "nezachet",
	"comment", "source", "author", "number", "setcounter",
}

// NumbersHandling mirrors args.numbers_handling.
type NumbersHandling string

const (
	// NumbersDefault only writes a number when it is zero (a warm-up question) or
	// when the package doesn't start at 1.
	NumbersDefault NumbersHandling = "default"
	// NumbersAll writes every question's number.
	NumbersAll NumbersHandling = "all"
)

var reDoubleSep = regexp.MustCompile("\n+")

func removeDoubleSeparators(s string) string { return reDoubleSep.ReplaceAllString(s, "\n") }

// formatElement ports compose_4s.format_element: a plain string passes through,
// while a list becomes 4s "- " items, optionally preceded by a preamble.
func formatElement(v any) string {
	switch t := v.(type) {
	case string:
		return removeDoubleSeparators(t)
	case []any:
		// The [preamble, [items…]] shape.
		if len(t) == 2 {
			if items, ok := t[1].([]any); ok {
				return removeDoubleSeparators(toString(t[0])) + "\n- " + joinItems(items)
			}
		}
		return "\n- " + joinItems(t)
	case int:
		return strconv.Itoa(t)
	}
	return fmt.Sprint(v)
}

func joinItems(items []any) string {
	parts := make([]string, len(items))
	for i, x := range items {
		parts[i] = removeDoubleSeparators(toString(x))
	}
	return strings.Join(parts, "\n- ")
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int:
		return strconv.Itoa(t)
	}
	return fmt.Sprint(v)
}

// isZero ports compose_4s.is_zero: a number counts as "zero" when it starts with
// a 0 or isn't an integer at all.
func isZero(v any) bool {
	s := toString(v)
	if strings.HasPrefix(s, "0") {
		return true
	}
	_, err := strconv.Atoi(strings.TrimSpace(s))
	return err != nil
}

func tryInt(v any) (int, bool) {
	if n, ok := v.(int); ok {
		return n, true
	}
	n, err := strconv.Atoi(strings.TrimSpace(toString(v)))
	return n, err == nil
}

// Compose ports common.compose_4s: it renders a parsed structure back to 4s
// source. Parse(Compose(d)) == d, which is what chgksuite's idempotence test
// asserts and what makes the import preview round-trippable.
func Compose(d Doc, numbers NumbersHandling) string {
	var out strings.Builder
	firstNumber := true

	for _, p := range d {
		if p.Type != "Question" {
			if marker, ok := composeMarkers[p.Type]; ok {
				value := p.Content
				// A theme parsed from 4s is an object; compose writes its label.
				if q, isQ := value.(*Question); isQ && p.Type == "theme" {
					value = q.Get("label")
				}
				out.WriteString(marker + formatElement(value) + "\n\n")
			}
			continue
		}
		q, ok := p.Content.(*Question)
		if !ok {
			continue
		}
		var tmp strings.Builder
		overrides, _ := q.Get("overrides").(map[string]string)
		if q.Has("number") {
			num := q.Get("number")
			switch numbers {
			case NumbersAll:
				tmp.WriteString("№ " + toString(num) + "\n")
			default:
				if isZero(num) {
					tmp.WriteString("№ " + toString(num) + "\n")
				} else if n, ok := tryInt(num); ok && firstNumber && n > 1 {
					tmp.WriteString("№№ " + toString(num) + "\n")
				}
			}
			if !isZero(num) {
				firstNumber = false
			}
		}
		for _, label := range composeOrder {
			marker, hasMarker := composeMarkers[label]
			if !hasMarker || !q.Has(label) {
				continue
			}
			override := ""
			if ov, ok := overrides[label]; ok {
				override = "!!" + ov + " "
			}
			tmp.WriteString(marker + override + formatElement(q.Get(label)) + "\n")
		}
		out.WriteString(reDoubleSep.ReplaceAllString(tmp.String(), "\n") + "\n")
	}
	return out.String()
}
