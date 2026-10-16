// Package textparse turns the plain text of a question package into the
// structure the 4s exporters consume, as chgksuite's parser.py does, so xy can
// import a .docx or .txt package in-process. parse.go reads chgk and brain
// packages, si.go and troika.go the СИ and троика ones, db.go db.chgk.info's
// export. Each chgksuite canon must come out byte for byte (parse_test.go).
package textparse

import (
	"strings"
	"time"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/fsource"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/typo"
)

const sep = "\n"

// What "smart" mode takes for a package whose question numbers stand on lines
// of their own: between these many elements per numbered line, and numbers
// that follow each other at most numberLineGap apart.
const (
	minElemsPerNumber = 4.0
	maxElemsPerNumber = 13.0
	numberLineGap     = 3
)

// What searchForDate accepts as a package's date.
const (
	day         = 24 * time.Hour
	minDateYear = 1980
	// dateHorizon is how far ahead a date may lie, for a package announced early.
	dateHorizon = 365 * day
	// dateShare is the fraction of a header line the date must fill: 1/dateShare.
	dateShare = 10
)

// elem is one [type, content] pair of the working structure. content is a string
// for all but a handful of elements (a source can become a list of references).
type elem struct {
	Type    string
	Content any
}

func (e *elem) str() string {
	if s, ok := e.Content.(string); ok {
		return s
	}
	return ""
}

// creditsPatterns match, per language, a line of the editors' credits that
// stands above a tour's first question: the editor or the testers named, or
// someone thanked. Every language also takes the Russian words, which a
// translated package often keeps.
var creditsPatterns = map[string]string{
	"ru":     `^редактор|благодар|^тестировали|^тестеры`,
	"ua":     `^(тур\s+)?редактор|дяку|подяк|вдячн|^тестували|^тестери`,
	"by":     `^(тур\s+)?рэдакт|дзяку|падзяк|^тэсціравалі|^тэставалі`,
	"by_tar": `^(тур\s+)?рэдакт|дзяку|падзяк|^тэсціравалі|^тэставалі`,
	"kz_cyr": `^(тур\s+)?редактор|алғыс|рахмет`,
	"uz_cyr": `^(тур\s+)?муҳаррир|миннатдор|раҳмат|ташаккур`,
	"uz":     `^(tur\s+)?muharrir|minnatdor|rahmat|tashakkur`,
	"en":     `^edit(or|ed by)|^tested by|^testers\b|thank|grateful|gratitude`,
	"az":     `^redaktor|təşəkkür|minnətdar`,
	"sr":     `^(autori i\s+)?urednic|^urednik|zahval`,
	"ro":     `^redactor|mul[țţ]um|recunosc[ăa]to`,
}

// questionLabels mirrors common.QUESTION_LABELS — the fields that belong to a
// question rather than standing on their own in the structure.
var questionLabels = map[string]bool{
	"handout": true, "question": true, "answer": true, "zachet": true,
	"nezachet": true, "comment": true, "source": true, "author": true,
	"number": true, "setcounter": true,
}

// tourWords is ChgkParser.TOUR_NUMBERS_AS_WORDS: what --tour_numbers_as_words
// renames the tours to, whatever the document called them.
var tourWords = []string{
	"Первый", "Второй", "Третий", "Четвертый", "Пятый",
	"Шестой", "Седьмой", "Восьмой", "Девятый", "Десятый",
}

// zeroPrefixes are the two ways a package can mark an unnumbered warm-up question.
var zeroPrefixes = []string{"Нулевой вопрос", "Разминочный вопрос"}

// The Russian words the parser looks for or writes, besides the markers in
// i18n's regexes: questionStub is the label a number on a line of its own
// becomes, a duplet's or blitz's heading word marks it as a question, and the
// feminine author label is kept as a 4s label override.
const (
	questionStub         = "Вопрос "
	tourSuffix           = " тур"
	dupletWord           = "Дуплет."
	blitzWord            = "Блиц."
	dupletStem           = "дуплет"
	blitzStem            = "блиц"
	femaleAuthorLabel    = "авторка:"
	femaleAuthorOverride = "!!Авторка"
)

// isWarmup reports whether s names a warm-up question anywhere in it.
func isWarmup(s string) bool {
	low := strings.ToLower(s)
	for _, prefix := range zeroPrefixes {
		if strings.Contains(low, strings.ToLower(prefix)) {
			return true
		}
	}
	return false
}

type parser struct {
	structure []*elem
	opts      typo.Options
	rx        *regexSet
	// defaultAuthor is written into every question that names none.
	defaultAuthor string
	// now is "today" for the date sanity check; injectable so tests are stable.
	now time.Time
}

// Options controls the few knobs xy actually varies.
type Options struct {
	// SingleNumberLines mirrors args.single_number_line_handling
	// ("smart" | "on" | "off"). Empty means "smart", chgksuite's default.
	SingleNumberLines string
	// DefaultAuthor is --defaultauthor: whom a question with no author of its
	// own is credited to.
	DefaultAuthor string
	// TourNumbersAsWords is --tour_numbers_as_words: "the first tour" rather
	// than whatever the document called it.
	TourNumbersAsWords bool
	// Language is --language: which regexes_*.json names the field markers.
	// Empty is Russian.
	Language string
	// LabelsFile is --labels_file: a labels TOML of one's own, which the
	// handout rewrite reads its printed handout label from.
	LabelsFile string
	// Typo are the --typography_* switches. The zero value is every knob off,
	// so a caller that wants chgksuite's defaults says so.
	Typo typo.Options
}

// Parse ports ChgkParser.parse. text is the plain text of the package (from a
// .txt file or from docxread.ToText).
func Parse(text string, opts Options) fsource.Doc {
	// The smart modes look at the package once, before any of it is typographed.
	t := opts.Typo.Resolve(text)
	if opts.Language == "en" {
		// ChgkParser.__init__: English is typed with the quotes it wants.
		t.Quotes = typo.Off
	}
	p := &parser{opts: t, now: time.Now(), rx: mustRegexSet(opts.Language, opts.LabelsFile)}
	return p.parse(text, opts)
}

// parse reads a package in five stages, one file each: label.go types every
// line by its marker, starts.go finds the questions that carry none, group.go
// joins the unlabelled lines to their fields, fields.go cleans each field, and
// pack.go makes questions of them.
func (p *parser) parse(text string, opts Options) fsource.Doc {
	p.defaultAuthor = opts.DefaultAuthor
	p.structure = p.label(text)
	p.markQuestionStarts(opts.SingleNumberLines)
	p.structure = p.group(p.structure)
	if !p.cleanFields() {
		return nil
	}
	return p.finish(p.pack(), opts.TourNumbersAsWords)
}

// insert puts e at index i of the structure.
func (p *parser) insert(i int, e *elem) {
	p.structure = append(p.structure, nil)
	copy(p.structure[i+1:], p.structure[i:])
	p.structure[i] = e
}
