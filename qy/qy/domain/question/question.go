// Package question is a nominated question as qy keeps it: the chgksuite
// fields flattened to plain strings, parsed out of whatever a nominator pasted.
package question

import (
	"encoding/json"
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

// Links are the references a pasted question carries.
type Links struct {
	GotQuestionsID int64
	GotQuestions   string
	TournamentID   int64
}

func FindLinks(text string) Links {
	var l Links
	if m := gqLinkRe.FindStringSubmatch(text); m != nil {
		l.GotQuestions = m[0]
		l.GotQuestionsID, _ = idstr.Parse(m[1])
	}
	if m := tournamentLinkRe.FindStringSubmatch(text); m != nil {
		l.TournamentID, _ = idstr.Parse(m[1])
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
