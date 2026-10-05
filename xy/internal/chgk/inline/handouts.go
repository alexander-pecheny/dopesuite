package inline

import (
	"regexp"
	"strings"
)

// Piece is a stretch of question text, or a handout cut out of it: Label is
// the handout's caption and Text what it shows.
type Piece struct {
	Handout bool
	Label   string
	Text    string
}

var (
	reLeadingBreak  = regexp.MustCompile(`^[ \t]*\n`)
	reTrailingBreak = regexp.MustCompile(`\n[ \t]*$`)
)

// SplitHandouts cuts question text at the handouts that stand on lines of
// their own (composer_common.py split_handouts). The exporters set such a
// handout apart from the question as a captioned box; one in the middle of a
// sentence stays a part of the text, and so does a handout bracket with
// nothing after its label. The text pieces keep everything around the
// handouts except the line breaks that separated them from it. Text with no
// such handout comes back as the one piece it is.
func SplitHandouts(s string) []Piece {
	r := []rune(s)
	var pieces []Piece
	prev := 0
	for i := 0; i < len(r); {
		if isEscapedBracket(r, i) {
			i += 2
			continue
		}
		if r[i] != '[' {
			i++
			continue
		}
		end := matchingSquareBracket(r, i)
		if end < 0 {
			i++
			continue
		}
		body := string(r[i+1 : end])
		label, content, ok := strings.Cut(body, ":")
		content = strings.TrimSpace(content)
		if IsHandoutBody(body) && ok && content != "" && ownsLine(r, i, end+1) {
			pieces = append(pieces,
				Piece{Text: string(r[prev:i])},
				Piece{Handout: true, Label: strings.TrimSpace(label), Text: content})
			prev = end + 1
		}
		i = end + 1
	}
	if pieces == nil {
		return []Piece{{Text: s}}
	}
	pieces = append(pieces, Piece{Text: string(r[prev:])})
	var out []Piece
	for i, p := range pieces {
		if !p.Handout {
			if i > 0 {
				p.Text = reLeadingBreak.ReplaceAllString(p.Text, "")
			}
			if i < len(pieces)-1 {
				p.Text = reTrailingBreak.ReplaceAllString(p.Text, "")
			}
			if strings.TrimSpace(p.Text) == "" {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// ownsLine says whether r[start:end] has its line to itself: nothing but
// whitespace between it and the line breaks on either side.
func ownsLine(r []rune, start, end int) bool {
	lineStart := start
	for lineStart > 0 && r[lineStart-1] != '\n' {
		lineStart--
	}
	lineEnd := end
	for lineEnd < len(r) && r[lineEnd] != '\n' {
		lineEnd++
	}
	return strings.TrimSpace(string(r[lineStart:start])) == "" && strings.TrimSpace(string(r[end:lineEnd])) == ""
}

// QuestionPiece is a stretch of a question's value, or a handout to set apart:
// Value is a field value (a string, or the list forms fsource reads).
type QuestionPiece struct {
	Handout bool
	Label   string
	Value   any
}

// QuestionPieces ports composer_common.question_pieces: a question's text cut
// at its handouts, or nil when it has none. The handout of a field of its own
// (handout, captioned label) comes first; then the ones the question text
// holds on lines of their own (SplitHandouts).
func QuestionPieces(handout, question any, label string) []QuestionPiece {
	var pieces []QuestionPiece
	found := false
	if handout != nil {
		pieces = append(pieces, QuestionPiece{Handout: true, Label: label, Value: handout})
		found = true
	}
	switch v := question.(type) {
	case string:
		for _, pc := range SplitHandouts(v) {
			pieces = append(pieces, QuestionPiece{Handout: pc.Handout, Label: pc.Label, Value: pc.Text})
			found = found || pc.Handout
		}
	case nil:
	default:
		pieces = append(pieces, QuestionPiece{Value: v})
	}
	if !found {
		return nil
	}
	return pieces
}
