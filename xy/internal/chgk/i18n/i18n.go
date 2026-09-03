// Package i18n carries chgksuite's ten language sets: the labels an export
// prints («Вопрос», "Question", …) and the field-marker regexes a parser reads a
// package by. Both files are chgksuite's own, embedded verbatim, so the two
// tools cannot drift apart on a label or a marker.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"pecheny.me/dopecore/i18nstrings"
)

//go:embed assets/labels_*.toml assets/regexes_*.json
var assets embed.FS

// DefaultLanguage is what every command falls back to.
const DefaultLanguage = "ru"

// Languages are the ten chgksuite ships, in the order it lists them.
func Languages() []string {
	return []string{"ru", "en", "ua", "by", "by_tar", "uz", "uz_cyr", "kz_cyr", "az", "sr"}
}

// Labels is one labels_*.toml: the field headings and the handful of general
// strings an exporter needs. A key a language leaves out comes back empty, as
// it would from chgksuite's own dict.
type Labels struct {
	Question map[string]string
	General  map[string]string
}

// Field is labels["question_labels"][name].
func (l Labels) Field(name string) string { return l.Question[name] }

// Text is labels["general"][name].
func (l Labels) Text(name string) string { return l.General[name] }

var (
	mu       sync.Mutex
	labels   = map[string]Labels{}
	patterns = map[string]*Regexes{}
)

// LoadLabels reads a language's labels. An empty language is the default one.
func LoadLabels(language string) (Labels, error) {
	language = orDefault(language)
	mu.Lock()
	defer mu.Unlock()
	if l, ok := labels[language]; ok {
		return l, nil
	}
	raw, err := assets.ReadFile("assets/labels_" + language + ".toml")
	if err != nil {
		return Labels{}, fmt.Errorf("язык %q: нет набора подписей", language)
	}
	l, err := parseLabels(string(raw))
	if err != nil {
		return Labels{}, fmt.Errorf("labels_%s.toml: %w", language, err)
	}
	labels[language] = l
	return l, nil
}

// LabelsFor is LoadLabels with --labels_file taken into account: a file of
// one's own replaces the language's set.
//
// chgksuite declares that flag and then overwrites it from --language on the
// way in, so upstream it does nothing and --language custom, which is what it
// was meant to pair with, has no set to load and crashes. Here it works.
func LabelsFor(language, file string) (Labels, error) {
	if file == "" {
		return LoadLabels(language)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return Labels{}, err
	}
	l, err := parseLabels(string(raw))
	if err != nil {
		return Labels{}, fmt.Errorf("%s: %w", file, err)
	}
	return l, nil
}

// LabelsForOrDefault is LabelsFor for a caller with nowhere to put an error.
func LabelsForOrDefault(language, file string) Labels {
	if l, err := LabelsFor(language, file); err == nil {
		return l
	}
	return MustLabels(DefaultLanguage)
}

// Known reports whether a language has a set here. Commands check it, so the
// fallbacks below are only ever reached by a caller that skipped the check.
func Known(language string) bool {
	_, err := assets.ReadFile("assets/labels_" + orDefault(language) + ".toml")
	return err == nil
}

// LabelsOrDefault is LoadLabels for a caller with nowhere to put an error: an
// unknown language falls back to the default set rather than printing nothing.
func LabelsOrDefault(language string) Labels {
	if l, err := LoadLabels(language); err == nil {
		return l
	}
	return MustLabels(DefaultLanguage)
}

// MustLabels is LoadLabels for a language known to exist.
func MustLabels(language string) Labels {
	l, err := LoadLabels(language)
	if err != nil {
		panic(err)
	}
	return l
}

// parseLabels reads the two tables of flat string keys these files are.
func parseLabels(text string) (Labels, error) {
	l := Labels{Question: map[string]string{}, General: map[string]string{}}
	pairs, err := i18nstrings.Parse(text)
	if err != nil {
		return l, err
	}
	for _, p := range pairs {
		switch p.Table {
		case "question_labels":
			l.Question[p.Key] = p.Value
		case "general":
			l.General[p.Key] = p.Value
		}
	}
	return l, nil
}

// Regexes is one regexes_*.json, compiled. A key the file leaves out is nil,
// and every caller checks: chgksuite's own `if name in self.regexes`.
type Regexes struct {
	Language string
	byName   map[string]*regexp.Regexp
}

// Get returns the pattern under a key, or nil when the language has none.
func (r *Regexes) Get(name string) *regexp.Regexp {
	if r == nil {
		return nil
	}
	return r.byName[name]
}

// Has reports whether the language defines the key.
func (r *Regexes) Has(name string) bool { return r.Get(name) != nil }

// LoadRegexes reads and compiles a language's field-marker patterns.
func LoadRegexes(language string) (*Regexes, error) {
	language = orDefault(language)
	mu.Lock()
	defer mu.Unlock()
	if r, ok := patterns[language]; ok {
		return r, nil
	}
	raw, err := assets.ReadFile("assets/regexes_" + language + ".json")
	if err != nil {
		return nil, fmt.Errorf("язык %q: нет набора регулярных выражений", language)
	}
	var src map[string]string
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, fmt.Errorf("regexes_%s.json: %w", language, err)
	}
	r := &Regexes{Language: language, byName: make(map[string]*regexp.Regexp, len(src))}
	for name, pattern := range src {
		re, err := regexp.Compile(ForRE2(pattern))
		if err != nil {
			return nil, fmt.Errorf("regexes_%s.json, %s: %w", language, name, err)
		}
		r.byName[name] = re
	}
	patterns[language] = r
	return r, nil
}

// MustRegexes is LoadRegexes for a language known to exist.
func MustRegexes(language string) *Regexes {
	r, err := LoadRegexes(language)
	if err != nil {
		panic(err)
	}
	return r
}

// ForRE2 makes a Python pattern one Go can compile and mean the same by.
//
// The only systematic difference that bites is whitespace: Python's \s is
// Unicode-aware and matches the NBSP that .docx text is full of, and Go's is
// ASCII-only. Every \s therefore takes U+00A0 with it.
//
// Python's $ also matches just before a trailing newline where Go's does not,
// but every pattern here is applied to one already-trimmed line, so the two are
// the same in practice.
func ForRE2(pattern string) string {
	var out strings.Builder
	inClass := false
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case c == '\\' && i+1 < len(pattern):
			if pattern[i+1] == 's' {
				if inClass {
					out.WriteString(`\s\x{00a0}`)
				} else {
					out.WriteString(`[\s\x{00a0}]`)
				}
			} else {
				out.WriteByte(c)
				out.WriteByte(pattern[i+1])
			}
			i++
		case c == '[' && !inClass:
			inClass = true
			out.WriteByte(c)
		case c == ']' && inClass:
			inClass = false
			out.WriteByte(c)
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

func orDefault(language string) string {
	if language == "" {
		return DefaultLanguage
	}
	return language
}

// QuestionLabel is get_label's question/tour branch: most languages put the
// number after the label, Uzbek before it. chgksuite also has a "kz" branch,
// which no --language value reaches — the Kazakh set is called kz_cyr.
func QuestionLabel(label, number, language string) string {
	switch language {
	case "uz", "uz_cyr":
		return number + " – " + label
	default:
		return label + " " + number
	}
}
