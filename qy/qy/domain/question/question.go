// Package question is a nominated question as qy keeps it: the chgksuite
// fields flattened to plain strings, parsed out of whatever a nominator pasted.
package question

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/fsource"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/textparse"
)

type Question struct {
	Text     string `json:"text,omitempty"`
	Answer   string `json:"answer,omitempty"`
	Zachet   string `json:"zachet,omitempty"`
	Nezachet string `json:"nezachet,omitempty"`
	Comment  string `json:"comment,omitempty"`
	Source   string `json:"source,omitempty"`
	Author   string `json:"author,omitempty"`
}

// Field names, as stored in suggestions.field and posted by the review form.
const (
	FieldText     = "text"
	FieldAnswer   = "answer"
	FieldZachet   = "zachet"
	FieldNezachet = "nezachet"
	FieldComment  = "comment"
	FieldSource   = "source"
	FieldAuthor   = "author"
)

// Fields are the editable fields in reading order. Author is last and is never
// shown to the jury.
var Fields = []string{FieldText, FieldAnswer, FieldZachet, FieldNezachet, FieldComment, FieldSource, FieldAuthor}

// Ptr answers the field named f, or nil for an unknown name.
func (q *Question) Ptr(f string) *string {
	switch f {
	case FieldText:
		return &q.Text
	case FieldAnswer:
		return &q.Answer
	case FieldZachet:
		return &q.Zachet
	case FieldNezachet:
		return &q.Nezachet
	case FieldComment:
		return &q.Comment
	case FieldSource:
		return &q.Source
	case FieldAuthor:
		return &q.Author
	}
	return nil
}

func (q Question) Get(f string) string {
	if p := q.Ptr(f); p != nil {
		return *p
	}
	return ""
}

// Anonymous is the copy the jury reads: everything but the credits.
func (q Question) Anonymous() Question {
	q.Author = ""
	return q
}

func (q Question) Empty() bool { return q.Text == "" && q.Answer == "" }

func (q Question) JSON() string {
	b, _ := json.Marshal(q)
	return string(b)
}

func FromJSON(s string) Question {
	var q Question
	_ = json.Unmarshal([]byte(s), &q)
	return q
}

// Parse reads one question out of pasted text. It drops the lines that say
// which tournament or link the question came from, then hands the rest to the
// chgksuite parser. ok is false when no answer could be found.
func Parse(text string) (Question, bool) {
	var kept []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if !isMetaLine(line) {
			kept = append(kept, line)
		}
	}
	body := strings.TrimSpace(strings.Join(kept, "\n"))
	if body == "" {
		return Question{}, false
	}
	for _, p := range textparse.Parse(body, textparse.Options{}) {
		fq, isQ := p.Content.(*fsource.Question)
		if p.Type != "Question" || !isQ {
			continue
		}
		q := Question{
			Text:     flatten(fq.Get("question")),
			Answer:   flatten(fq.Get("answer")),
			Zachet:   flatten(fq.Get("zachet")),
			Nezachet: flatten(fq.Get("nezachet")),
			Comment:  flatten(fq.Get("comment")),
			Source:   flatten(fq.Get("source")),
			Author:   flatten(fq.Get("author")),
		}
		return q, q.Answer != ""
	}
	return Question{}, false
}

// flatten turns a chgksuite field value into text: a list becomes numbered
// lines, and the [preamble, items] form keeps its preamble on the first line.
func flatten(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case []any:
		if len(x) == 2 {
			if pre, ok := x[0].(string); ok {
				if items, ok := x[1].([]any); ok {
					return strings.TrimSpace(pre + "\n" + numbered(items))
				}
			}
		}
		return numbered(x)
	}
	return ""
}

func numbered(items []any) string {
	if len(items) == 1 {
		return flatten(items[0])
	}
	lines := make([]string, 0, len(items))
	for i, it := range items {
		lines = append(lines, strconv.Itoa(i+1)+". "+flatten(it))
	}
	return strings.Join(lines, "\n")
}

// NormalAnswer is the answer reduced to letters and digits, the key two
// nominations of one question are likely to share.
func NormalAnswer(answer string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(answer) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return yo.Replace(b.String())
}

// minSameWords is how many words each question needs before Same will call
// two of them one: a bare link or a stub is not enough to go on.
const minSameWords = 8

// sameAt is the similarity at which two questions are one. The pairs nominated
// twice in 2026 scored 1.0, and the closest two different questions 0.45; the
// gap leaves room for one copy with a typo fixed or a credit dropped.
const sameAt = 0.9

// Same says whether two questions are one question nominated twice: their
// texts and answers have nearly the same words, counted as a bag (cosine of
// the word counts), ignoring case, stress marks and the diaeresis on e.
func Same(a, b Question) bool {
	x, y := wordBag(a), wordBag(b)
	if len(x) == 0 || len(y) == 0 || total(x) < minSameWords || total(y) < minSameWords {
		return false
	}
	var dot, nx, ny float64
	for w, n := range x {
		dot += float64(n * y[w])
		nx += float64(n * n)
	}
	for _, n := range y {
		ny += float64(n * n)
	}
	return dot/math.Sqrt(nx*ny) >= sameAt
}

func wordBag(q Question) map[string]int {
	bag := map[string]int{}
	words := strings.FieldsFunc(yo.Replace(strings.ToLower(withoutNotes(q.Text)+" "+withoutNotes(q.Answer))), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.Is(unicode.Mn, r)
	})
	for _, w := range words {
		if w = strings.Map(func(r rune) rune {
			if unicode.Is(unicode.Mn, r) {
				return -1
			}
			return r
		}, w); w != "" {
			bag[w]++
		}
	}
	return bag
}

// notesRe are what a question carries besides itself: a note in brackets,
// to the host or on how a word is said, and a link.
var notesRe = regexp.MustCompile(`\[[^\[\]]*\]|\{[^{}]*\}|https?://\S+`)

// withoutNotes is a question's text as the players hear it: its handout and
// its words, without the notes one copy may have and another not.
func withoutNotes(text string) string {
	handout, rest := SplitHandout(text)
	return handout + " " + notesRe.ReplaceAllString(rest, " ")
}

func total(bag map[string]int) int {
	n := 0
	for _, c := range bag {
		n += c
	}
	return n
}

// Links are the references a pasted question carries.
type Links struct {
	GotQuestionsID int64
	GotQuestions   string
	TournamentID   int64
	// Number is the question's number in the tournament, when the link
	// names it after "#", as a link made for the preliminary sheet does.
	Number int
}

func FindLinks(text string) Links {
	var l Links
	if m := gqLinkRe.FindStringSubmatch(text); m != nil {
		l.GotQuestions = m[0]
		l.GotQuestionsID, _ = idstr.Parse(m[1])
	}
	if m := tournamentLinkRe.FindStringSubmatch(text); m != nil {
		l.TournamentID, _ = idstr.Parse(m[1])
		l.Number, _ = strconv.Atoi(m[2])
	}
	return l
}

// AuthorNames splits a credit line into the names it gives, without the
// cities in parentheses.
func AuthorNames(author string) []string {
	author = parenRe.ReplaceAllString(author, "")
	var out []string
	for _, part := range authorSplitRe.Split(author, -1) {
		if p := strings.TrimSpace(strings.Trim(part, ".;")); p != "" {
			out = append(out, p)
		}
	}
	return out
}
