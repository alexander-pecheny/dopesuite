package question

import (
	"regexp"
	"strings"
)

// The markers that match Russian input, kept in one file so the Cyrillic
// allowlist names only this one.

// metaLines are the lines a question copied from a tournament page or from
// gotquestions opens with, which say where it came from.
var metaLineRe = regexp.MustCompile(`(?i)^\s*(название турнира|турнир|ссылка на турнир|ссылка на вопрос)\s*:`)

func isMetaLine(line string) bool {
	return metaLineRe.MatchString(line) || gqLinkRe.MatchString(strings.TrimSpace(line)) && len(strings.Fields(line)) == 1
}

var (
	gqLinkRe         = regexp.MustCompile(`https?://(?:www\.)?gotquestions\.online/question/(\d+)`)
	tournamentLinkRe = regexp.MustCompile(`rating\.chgk\.info/tournament/(\d+)`)
	parenRe          = regexp.MustCompile(`\([^)]*\)`)
	authorSplitRe    = regexp.MustCompile(`\s*,\s*|\s+и\s+`)
	yo               = strings.NewReplacer("ё", "е")
)
