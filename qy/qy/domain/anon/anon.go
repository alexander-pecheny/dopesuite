// Package anon finds what in a question still gives its author or tournament
// away, and proposes a redaction for each find. A rule pass catches the
// mechanical leaks; a language model, asked through Prompt, catches the rest.
// Nothing here edits a question: an admin accepts or rejects each Suggestion.
package anon

import (
	_ "embed"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"qy/qy/domain/question"
)

// Suggestion is one proposed edit: replace Quote in Field with Replacement.
type Suggestion struct {
	Field       string `json:"field"`
	Quote       string `json:"quote"`
	Replacement string `json:"replacement"`
	Reason      string `json:"reason"`
}

// The fields the jury reads, which are the only ones worth redacting.
var juryFields = []string{
	question.FieldText, question.FieldAnswer, question.FieldZachet,
	question.FieldNezachet, question.FieldComment, question.FieldSource,
}

// Reasons the rule pass gives; the server shows them in the admin's words.
const (
	ReasonAuthorName = "author-name"
	ReasonLink       = "link"
)

// sourceLinkRe matches the pages that name a question's tournament.
var sourceLinkRe = regexp.MustCompile(`https?://(?:www\.)?(?:gotquestions\.online/(?:question|pack)/\d+|rating\.chgk\.info/tournaments?/\d+)\S*`)

// Rules runs the mechanical checks against q, whose Author names the people
// to look for. placeholder is what an author's name is replaced with.
func Rules(q question.Question, placeholder string) []Suggestion {
	stems := nameStems(question.AuthorNames(q.Author))
	var out []Suggestion
	for _, f := range juryFields {
		text := q.Get(f)
		for _, quote := range nameRuns(text, stems) {
			out = append(out, Suggestion{f, quote, placeholder, ReasonAuthorName})
		}
		if f == question.FieldSource {
			continue
		}
		for _, link := range sourceLinkRe.FindAllString(text, -1) {
			out = append(out, Suggestion{f, link, "", ReasonLink})
		}
	}
	return dedupe(out)
}

// The shortest name part worth matching: shorter ones are initials and
// particles that would match half the language.
const minNameRunes = 4

// The longest stem kept whole. Longer parts lose their last letter, so that
// Ivanova also finds Ivanovoy, and Eduard finds Eduarda.
const wholeStemRunes = 5

// maxEnding is how many letters a declined name may add to its stem.
const maxEnding = 3

func nameStems(names []string) []string {
	var stems []string
	for _, n := range names {
		for _, part := range strings.Fields(n) {
			part = strings.ToLower(strings.TrimFunc(part, func(r rune) bool { return !unicode.IsLetter(r) }))
			rs := []rune(part)
			if len(rs) < minNameRunes {
				continue
			}
			if len(rs) > wholeStemRunes {
				rs = rs[:len(rs)-1]
			}
			stems = append(stems, string(rs))
		}
	}
	return stems
}

type word struct {
	start, end int
	match      bool
}

// nameRuns finds the words of text that are declined forms of a stem, and
// joins neighbours separated only by spaces into one quote, so a first name
// and a surname become one redaction rather than two.
func nameRuns(text string, stems []string) []string {
	if len(stems) == 0 {
		return nil
	}
	var words []word
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if !unicode.IsLetter(r) {
			i += size
			continue
		}
		j := i
		for j < len(text) {
			r, size := utf8.DecodeRuneInString(text[j:])
			if !unicode.IsLetter(r) && r != '-' {
				break
			}
			j += size
		}
		words = append(words, word{i, j, isName(text[i:j], stems)})
		i = j
	}
	var out []string
	for k := 0; k < len(words); k++ {
		if !words[k].match {
			continue
		}
		end := k
		for end+1 < len(words) && words[end+1].match && strings.TrimSpace(text[words[end].end:words[end+1].start]) == "" {
			end++
		}
		out = append(out, text[words[k].start:words[end].end])
		k = end
	}
	return out
}

func isName(w string, stems []string) bool {
	low := strings.ToLower(w)
	n := utf8.RuneCountInString(low)
	for _, s := range stems {
		if strings.HasPrefix(low, s) && n-utf8.RuneCountInString(s) <= maxEnding {
			return true
		}
	}
	return false
}

func dedupe(in []Suggestion) []Suggestion {
	seen := map[[2]string]bool{}
	var out []Suggestion
	for _, s := range in {
		k := [2]string{s.Field, s.Quote}
		if !seen[k] {
			seen[k] = true
			out = append(out, s)
		}
	}
	return out
}

// ErrNotFound is Apply's answer when the quote is no longer in the field,
// because somebody edited it since the suggestion was made.
var ErrNotFound = errors.New("quote not found in field")

// Apply makes the edit a suggestion proposes, on its first occurrence.
func Apply(q question.Question, s Suggestion) (question.Question, error) {
	p := q.Ptr(s.Field)
	if p == nil || s.Quote == "" || !strings.Contains(*p, s.Quote) {
		return q, ErrNotFound
	}
	*p = tidy(strings.Replace(*p, s.Quote, s.Replacement, 1))
	return q, nil
}

var doubleSpaceRe = regexp.MustCompile(`[ \t]{2,}`)

// tidy closes the gap a deletion leaves behind.
func tidy(s string) string { return strings.TrimSpace(doubleSpaceRe.ReplaceAllString(s, " ")) }

//go:embed prompt.md
var promptTemplate string

// Prompt is the instruction and the material for a model, which answers with
// the JSON ParseReply reads.
func Prompt(q question.Question, tournament, placeholder string) (system, user string) {
	system = strings.ReplaceAll(promptTemplate, "{{placeholder}}", placeholder)
	fields := map[string]string{}
	for _, f := range juryFields {
		if v := q.Get(f); v != "" {
			fields[f] = v
		}
	}
	material, _ := json.MarshalIndent(map[string]any{
		"authors":    question.AuthorNames(q.Author),
		"tournament": tournament,
		"fields":     fields,
	}, "", "  ")
	return system, string(material)
}

// ParseReply reads a model's answer, keeping only the suggestions that name a
// field the jury reads and quote it exactly.
func ParseReply(reply string, q question.Question) ([]Suggestion, error) {
	start, end := strings.Index(reply, "{"), strings.LastIndex(reply, "}")
	if start < 0 || end < start {
		return nil, errors.New("no JSON object in the reply")
	}
	var parsed struct {
		Suggestions []Suggestion `json:"suggestions"`
	}
	if err := json.Unmarshal([]byte(reply[start:end+1]), &parsed); err != nil {
		return nil, err
	}
	var out []Suggestion
	for _, s := range parsed.Suggestions {
		if isJuryField(s.Field) && s.Quote != "" && s.Quote != s.Replacement && strings.Contains(q.Get(s.Field), s.Quote) {
			out = append(out, s)
		}
	}
	return dedupe(out), nil
}

func isJuryField(f string) bool {
	for _, j := range juryFields {
		if f == j {
			return true
		}
	}
	return false
}
