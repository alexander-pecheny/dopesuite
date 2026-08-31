// Package openquiz is composer/openquiz.py: a package as the JSON open-quiz.com
// imports, one object per question.
package openquiz

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/imghost"
	"xy/internal/chgk/inline"
)

// Media is a picture openquiz shows beside a question or its answer.
type Media struct {
	Key  string `json:"Key"`
	Type string `json:"Type"`
}

// Question is one entry of the exported array. The field names and their order
// are openquiz's own.
type Question struct {
	Single Single `json:"Single"`
}

type Single struct {
	Caption       string  `json:"Caption"`
	Question      Body    `json:"Question"`
	QuestionMedia *Media  `json:"QuestionMedia"`
	Answer        Answer  `json:"Answer"`
	AnswerMedia   *Media  `json:"AnswerMedia"`
	Comment       string  `json:"Comment"`
	Points        string  `json:"Points"`
	JeopardyPnts  *string `json:"JeopardyPoints"`
	WithChoice    bool    `json:"WithChoice"`
	Seconds       *int    `json:"Seconds"`
	EndOfTour     bool    `json:"EndOfTour"`
}

// Body is openquiz's question text: one block, or the parts of a blitz.
type Body struct {
	Solid string   `json:"Solid,omitempty"`
	Split []string `json:"Split,omitempty"`
}

type Answer struct {
	OpenAnswer string `json:"OpenAnswer"`
}

// Export renders a package as openquiz JSON. images maps a picture's name to its
// bytes; a directive that already names a URL is left alone, anything else goes
// to host.
func Export(doc fsource.Doc, images map[string][]byte, host imghost.Host, o Options) ([]byte, error) {
	e := &exporter{images: images, host: host, opts: o}
	out := []Question{}
	for i, q := range questionsAndTours(doc) {
		if q.q == nil {
			continue
		}
		item, err := e.question(q.q, q.endOfTour, i)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return marshalIndent(out)
}

type slot struct {
	q         *fsource.Question
	endOfTour bool
}

// questionsAndTours walks the questions and the section headings between them:
// the last question before a heading, and the last of all, end a tour.
func questionsAndTours(doc fsource.Doc) []slot {
	var slots []slot
	for _, p := range doc {
		switch {
		case p.Type == "Question":
			q, _ := p.Content.(*fsource.Question)
			slots = append(slots, slot{q: q})
		case p.Type == "section":
			slots = append(slots, slot{})
		}
	}
	for i := range slots {
		if i+1 == len(slots) || slots[i+1].q == nil {
			slots[i].endOfTour = true
		}
	}
	return slots
}

type exporter struct {
	images map[string][]byte
	host   imghost.Host
	opts   Options
}

// Options are the switches this export reads: only the non-breaking ones.
type Options struct {
	NoBreak inline.NoBreak
}

var (
	reHandoutShort  = regexp.MustCompile(`[Рр][Аа][Зз][Дд][Аа][Тт]`)
	reHandoutBlock  = regexp.MustCompile(`(?s)\[[Рр][Аа][Зз][Дд][Аа][Тт](.+?)\]`)
	reHandoutInline = regexp.MustCompile(`(?s)\[[Рр][Аа][Зз][Дд][Аа][Тт].+?: ?(.+)\]`)
	reCommaRun      = regexp.MustCompile(`, *`)
	reBrackets      = regexp.MustCompile(`\[.+\]`)
)

// question is oq_format_question.
func (e *exporter) question(q *fsource.Question, endOfTour bool, index int) (Question, error) {
	item := Question{Single: Single{Caption: "31", Points: "1"}}

	var questionImages []string
	switch split := makeSplit(q.Get("question"), false).(type) {
	case []any:
		parts := make([]string, len(split))
		for i, s := range split {
			text, imgs, err := e.format(toString(s), true)
			if err != nil {
				return item, err
			}
			parts[i], questionImages = text, append(questionImages, imgs...)
		}
		item.Single.Question.Split = parts
	default:
		text, imgs, err := e.format(toString(split), true)
		if err != nil {
			return item, err
		}
		item.Single.Question.Solid, questionImages = strings.TrimSpace(text), imgs
	}
	if len(questionImages) > 0 {
		item.Single.QuestionMedia = &Media{Key: questionImages[0], Type: "Picture"}
	}

	answer := toString(makeSplit(q.Get("answer"), true))
	if q.Has("zachet") {
		answer += "\n" + toString(makeSplit(q.Get("zachet"), true))
	}
	formatted, answerImages, err := e.format(cleanAnswer(answer), false)
	if err != nil {
		return item, err
	}
	item.Single.Answer.OpenAnswer = formatted

	if q.Has("comment") {
		text, imgs, err := e.format(toString(makeSplit(q.Get("comment"), true)), true)
		if err != nil {
			return item, err
		}
		item.Single.Comment, answerImages = text, append(answerImages, imgs...)
	}
	if len(answerImages) > 0 {
		item.Single.AnswerMedia = &Media{Key: answerImages[0], Type: "Picture"}
	}

	item.Single.Caption = fmt.Sprintf("%v", q.Get("number"))
	item.Single.EndOfTour = endOfTour
	return item, nil
}

// cleanAnswer is what openquiz wants of an answer: one acceptable wording per
// line, brackets both kept and dropped so either spelling scores.
func cleanAnswer(answer string) string {
	answer = reCommaRun.ReplaceAllString(answer, "\n")
	answer = strings.ReplaceAll(answer, ".\n", "\n")
	answer = strings.TrimSuffix(answer, ".")

	var lines []string
	for _, x := range strings.Split(answer, "\n") {
		if x = strings.TrimSpace(x); x != "" && x != "точный ответ" {
			lines = append(lines, x)
		}
	}
	for i := 0; i < len(lines); i++ {
		x := lines[i]
		stripped := x
		for {
			loc := reBrackets.FindString(stripped)
			if loc == "" {
				break
			}
			stripped = strings.Replace(stripped, loc, "", 1)
		}
		if stripped == x {
			continue
		}
		lines[i] = strings.NewReplacer("[", "", "]", "").Replace(x)
		lines = append(lines[:i+1], append([]string{strings.TrimSpace(stripped)}, lines[i+1:]...)...)
		i++
	}
	return strings.Join(lines, "\n")
}

// format is oqformat: openquiz takes plain text, so every run is flattened, and
// a picture becomes a URL beside the question rather than part of it.
func (e *exporter) format(s string, removeBrackets bool) (string, []string, error) {
	if removeBrackets {
		s = inline.RemoveSquareBrackets(s)
	}
	var b strings.Builder
	var images []string
	for _, r := range inline.Parse4sElem(s) {
		switch r.Kind {
		case "", "hyperlink", "italic":
			b.WriteString(r.Text)
		case "screen":
			b.WriteString(r.ForScreen)
		case "img":
			link, err := e.link(r.Text)
			if err != nil {
				return "", nil, err
			}
			images = append(images, link)
		}
	}
	res := strings.TrimRight(b.String(), "\n")
	switch {
	case len(images) > 0:
		// The handout was a picture, so its brackets go with the text.
		res = strings.TrimSpace(reHandoutBlock.ReplaceAllString(s, ""))
	case reHandoutShort.MatchString(res):
		if m := reHandoutInline.FindStringSubmatch(res); m != nil {
			res = strings.Replace(res, m[0], m[1], 1)
		}
	}
	res = inline.ReplaceNoBreak(res, e.opts.NoBreak)
	return strings.ReplaceAll(res, "́", ""), images, nil
}

func (e *exporter) link(arg string) (string, error) {
	if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
		return arg, nil
	}
	name := arg
	if img, ok := inline.ParseImg(arg); ok {
		name = img.Name
	}
	data, found := e.images[name]
	if !found || e.host == nil {
		return "", nil
	}
	return e.host.Upload(name, data)
}

// makeSplit is openquiz's make_split: a list becomes numbered lines, and the
// [preamble, [items…]] shape glues the preamble onto the first of them.
func makeSplit(v any, join bool) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}
	var result any = nil
	if len(list) == 1 {
		result = list[0]
	}
	if len(list) > 1 {
		if inner, nested := list[1].([]any); nested {
			items := numbered(inner)
			if len(items) > 0 {
				items[0] = toString(list[0]) + "\n" + items[0]
			}
			result = toAny(items)
		} else {
			result = toAny(numbered(list))
		}
	}
	if !join {
		return result
	}
	if items, isList := result.([]any); isList {
		parts := make([]string, len(items))
		for i, x := range items {
			parts[i] = toString(x)
		}
		return strings.Join(parts, "\n")
	}
	return result
}

func numbered(list []any) []string {
	out := make([]string, len(list))
	for i, x := range list {
		out[i] = fmt.Sprintf("%d. %s", i+1, toString(x))
	}
	return out
}

func toAny(items []string) []any {
	out := make([]any, len(items))
	for i, s := range items {
		out[i] = s
	}
	return out
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// marshalIndent writes the JSON as Python's json.dumps(indent=2,
// ensure_ascii=False) does: two spaces and no escaping of anything but what
// JSON requires.
func marshalIndent(v any) ([]byte, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return []byte(strings.TrimSuffix(b.String(), "\n")), nil
}
