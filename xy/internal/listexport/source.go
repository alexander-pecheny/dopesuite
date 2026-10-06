package listexport

import (
	"strings"

	"xy/internal/cardkind"
)

// The 4s document a List exports as. Every format is rendered from this one
// string, which is why the Versions are folded back into one question here and
// nowhere else.

// source is the Cards' descriptions in board order, blank-line separated. A
// handouts preamble is for the handouts alone.
func source(cards []Card) string {
	var parts []string
	for _, c := range cards {
		if !cardkind.Exported(c.Kind) {
			continue
		}
		s := foldBlankLines(withQuestionMarker(c.Kind, withKindMarker(c.Kind, strings.TrimSpace(composeVersions(c.Desc)))))
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n") + "\n"
}

// kindMarker is the marker a plain-text heading or meta Card stands for: the
// board shows it by its kind, so nobody has to type a `##` or a `#`, while 4s
// drops a line that has no marker and follows nothing. A heading becomes a `##`
// section, which is what restarts the theme count in SI, as the board's
// numbering does after a heading. Which kind stands for which marker is
// cardkind's to say.
func withKindMarker(kind, desc string) string {
	marker := cardkind.Of(kind).Marker
	if marker == "" || desc == "" {
		return desc
	}
	if _, _, isMarker := MatchMarker(strings.SplitN(desc, "\n", 2)[0]); isMarker {
		return desc
	}
	return marker + " " + desc
}

// lineType is the kind of element a line opens on its own: "" for a blank line
// or a Version separator, "pre" for text with no marker.
func lineType(l string) string {
	if strings.TrimSpace(l) == "" {
		return ""
	}
	if _, isVersion := VersionLineName(l); isVersion {
		return ""
	}
	if k, _, ok := MatchMarker(l); ok {
		return k
	}
	return "pre"
}

// withQuestionMarker gives a question written with no marker its `?`. The card
// reads such text at the top of a question, or right under a theme's `№`, as the
// question itself; 4s continues the element above it with it, so the question
// would never close and would swallow everything up to the next theme.
func withQuestionMarker(kind, desc string) string {
	if !cardkind.Numbered(kind) {
		return desc
	}
	lines := strings.Split(desc, "\n")
	// Each part is one question: the lines under a `№`, or for a question Card
	// also the lines above the first one. A theme's head is not a question.
	var starts []int
	if kind == cardkind.Question {
		starts = append(starts, 0)
	}
	for i, l := range lines {
		if lineType(l) == "number" {
			starts = append(starts, i+1)
		}
	}
	for k, start := range starts {
		end := len(lines)
		if k+1 < len(starts) {
			end = starts[k+1] - 1
		}
		first, hasQuestion := -1, false
		for i := start; i < end; i++ {
			t := lineType(lines[i])
			if t == "question" {
				hasQuestion = true
			}
			if first < 0 && t != "" {
				first = i
			}
		}
		if !hasQuestion && first >= 0 && lineType(lines[first]) == "pre" {
			lines[first] = "? " + lines[first]
		}
	}
	return strings.Join(lines, "\n")
}

// foldBlankLines turns each blank line inside a Card into chgksuite's explicit
// (LINEBREAK) on the end of the line before: to 4s a blank line ends the
// element, so everything past it would fall out of the export. Before a marker
// the blank line separates nothing that is printed, so it just goes. Before a
// `№` it stays: it is what ends each rung of a theme's ladder, and without it
// the parser merges the rungs into one question numbered «1020» (#81).
func foldBlankLines(desc string) string {
	var out []string
	blanks := 0
	for _, line := range strings.Split(desc, "\n") {
		if strings.TrimSpace(line) == "" {
			blanks++
			continue
		}
		if blanks > 0 && len(out) > 0 {
			_, _, isMarker := MatchMarker(line)
			switch {
			case opensQuestion(line):
				out = append(out, "")
			case !isMarker:
				out[len(out)-1] += strings.Repeat("(LINEBREAK)", blanks)
			}
		}
		out = append(out, line)
		blanks = 0
	}
	return strings.Join(out, "\n")
}
