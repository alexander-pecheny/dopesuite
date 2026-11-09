package question

import (
	"regexp"
	"strconv"
	"strings"
)

// The markers that match Russian input, kept in one file so the Cyrillic
// allowlist names only this one.

// metaLines are the lines a question copied from a tournament page or from
// gotquestions opens with, which say where it came from.
var metaLineRe = regexp.MustCompile(`(?i)^\s*(название турнира|турнир|ссылка на турнир|ссылка на вопрос)\s*:`)

// statsLineRe is the line a channel closes a played question with: how many
// teams took it, "2/42", then who.
var statsLineRe = regexp.MustCompile(`^\s*\d+\s*/\s*\d+(\s|$)`)

// numberRe is the line a pasted question opens with, "Вопрос 12".
var numberRe = regexp.MustCompile(`(?im)^\s*вопрос\s+(\d+)\b`)

// Number is the question's number in its tournament, as a pasted text gives
// it, or 0.
func Number(text string) int {
	m := numberRe.FindStringSubmatch(text)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func isMetaLine(line string) bool {
	return metaLineRe.MatchString(line) || statsLineRe.MatchString(line) || gqLinkRe.MatchString(strings.TrimSpace(line)) && len(strings.Fields(line)) == 1
}

var (
	gqLinkRe         = regexp.MustCompile(`https?://(?:www\.)?gotquestions\.online/question/(\d+)`)
	tournamentLinkRe = regexp.MustCompile(`rating\.chgk\.info/tournaments?/(\d+)`)
	parenRe          = regexp.MustCompile(`\([^)]*\)`)
	authorSplitRe    = regexp.MustCompile(`\s*,\s*|\s+и\s+`)
	yo               = strings.NewReplacer("ё", "е")
)

// WithHandout puts a handout in front of the question text, the way a
// chgksuite paste carries it.
func WithHandout(text, handout string) string {
	if handout == "" {
		return text
	}
	return "[Раздаточный материал: " + handout + "]\n" + text
}

var handoutRe = regexp.MustCompile(`(?is)^\s*\[\s*раздаточный\s+материал\s*:?\s*(.*?)\]\s*\n?`)

// SplitHandout takes the handout WithHandout put in front of a question's
// text back out. A text that does not open with one is returned as it is.
func SplitHandout(text string) (handout, rest string) {
	m := handoutRe.FindStringSubmatchIndex(text)
	if m == nil {
		return "", text
	}
	return strings.TrimSpace(text[m[2]:m[3]]), strings.TrimSpace(text[m[1]:])
}

// formatLabels are the captions Format writes each field under, the ones
// Parse reads back.
var formatLabels = []struct{ field, label string }{
	{FieldAnswer, "Ответ"}, {FieldZachet, "Зачёт"}, {FieldNezachet, "Незачёт"},
	{FieldComment, "Комментарий"}, {FieldSource, "Источник"}, {FieldAuthor, "Автор"},
}

// Format writes a question as the text a nominator would paste, which Parse
// reads back into the same fields. q.Text may carry its handout in front.
func Format(q Question) string {
	if rest := FormatFields(q); rest != "" {
		return "Вопрос 1.\n" + q.Text + "\n" + rest
	}
	return "Вопрос 1.\n" + q.Text
}

// FormatFields writes the fields after the text, one "Label: value" a line.
func FormatFields(q Question) string {
	var lines []string
	for _, f := range formatLabels {
		if v := q.Get(f.field); v != "" {
			lines = append(lines, f.label+": "+v)
		}
	}
	return strings.Join(lines, "\n")
}
