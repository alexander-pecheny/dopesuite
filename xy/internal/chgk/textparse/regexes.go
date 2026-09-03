package textparse

import (
	"regexp"

	"xy/internal/chgk/i18n"
)

// The field-marker regexes are chgksuite's resources/regexes_*.json, compiled
// by the i18n package: it makes the two changes RE2 needs (Python's \s is
// Unicode-aware and matches the NBSP .docx text is full of; Go's is not) and
// nothing else. See i18n.ForRE2.
//
// ws is what those \s become, and is what the patterns written out by hand here
// and in si_regexes.go use.
const ws = `[\s\x{00a0}]`

// labelledOrder is the set apply_regexes tries, in the order chgksuite tries
// them: every key except the ones it excludes there ("number", "date2",
// "handout_short", and the si_* keys, which belong to the SI parser).
var labelledOrder = []string{
	"battle", "tour", "tourrev", "question", "handout", "answer",
	"zachet", "nezachet", "comment", "author", "source", "editor", "date",
}

// named is one field marker: its key and its pattern.
type named struct {
	name string
	re   *regexp.Regexp
}

// regexSet is one language's markers, ready for the parser. A language that
// names fewer keys than Russian simply has fewer: chgksuite's own `if name in
// self.regexes` is a nil check here.
type regexSet struct {
	*i18n.Regexes
	labelled []named
	// authorOnly is the author marker anchored whole-line (the bare label and
	// nothing else), used to splice the following element into it.
	authorOnly *regexp.Regexp
	// handoutLabel is the printed handout label, which the handout
	// rewrite looks for as text rather than as a marker.
	handoutLabel  string
	handoutBefore *regexp.Regexp
}

func newRegexSet(language, labelsFile string) (*regexSet, error) {
	rx, err := i18n.LoadRegexes(language)
	if err != nil {
		return nil, err
	}
	labels, err := i18n.LabelsFor(language, labelsFile)
	if err != nil {
		return nil, err
	}
	s := &regexSet{Regexes: rx, handoutLabel: labels.Field("handout")}
	for _, name := range labelledOrder {
		if re := rx.Get(name); re != nil {
			s.labelled = append(s.labelled, named{name, re})
		}
	}
	if re := rx.Get("author"); re != nil {
		s.authorOnly = regexp.MustCompile(`^(?:` + re.String() + `)$`)
	}
	s.handoutBefore = regexp.MustCompile(`(?s)` + regexp.QuoteMeta(s.handoutLabel) + `:([ \n]+)\[`)
	return s, nil
}

// field is the "regexes[k]" lookup, nil when this language names no such key.
func (s *regexSet) field(name string) *regexp.Regexp { return s.Get(name) }

// mustRegexSet is newRegexSet for a language the command has already checked
// against i18n.Known; an unknown one falls back to the default set rather than
// leaving the parser with no markers at all.
func mustRegexSet(language, labelsFile string) *regexSet {
	if s, err := newRegexSet(language, labelsFile); err == nil {
		return s
	}
	s, err := newRegexSet(i18n.DefaultLanguage, "")
	if err != nil {
		panic(err)
	}
	return s
}
