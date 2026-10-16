package textparse

import (
	"xy/internal/chgk/typo"
)

// group joins every untyped line to a field and gives every answer a question.
// A question runs up to its answer; a run of untyped lines that ends at an
// answer is its question; any other run continues the field before it or
// stays untyped, which later makes it meta.
func (p *parser) group(in []*elem) []*elem {
	var out []*elem
	for i := 0; i < len(in); {
		e := in[i]
		switch {
		case e.Type == "question":
			q := &elem{"question", e.Content}
			for i++; i < len(in) && !endsQuestion[in[i].Type]; i++ {
				appendLine(q, in[i].str())
			}
			out = append(out, q)
		case e.Type == "":
			j := i
			for j < len(in) && in[j].Type == "" {
				j++
			}
			next := ""
			if j < len(in) {
				next = in[j].Type
			}
			out = p.placeRun(out, in[i:j], next)
			i = j
		default:
			if e.Type == "answer" && !hasQuestion(out) && !answerSide[lastOf(out).Type] {
				out = insertQuestion(out, &elem{"question", ""})
			}
			out = append(out, e)
			i++
		}
	}
	for _, e := range out {
		if e.Type == "leadcomment" {
			e.Type = "comment"
		}
	}
	return out
}

var endsQuestion = map[string]bool{
	"answer": true, "leadcomment": true,
	"battle": true, "tour": true, "tourrev": true, "editor": true, "date": true,
}

func (p *parser) placeRun(out, run []*elem, next string) []*elem {
	prev := lastOf(out)
	if (next == "answer" || next == "leadcomment") && prev.Type != "leadcomment" {
		k, numbered := p.questionStart(run, prev)
		if !numbered && !questionLabels[prev.Type] {
			k = p.afterCredits(run)
		}
		out = continueOrKeep(out, run[:k], "question")
		q := &elem{"question", joinLines(run[k:]) + sep}
		if numbered {
			q.Content = p.rx.field("number").ReplaceAllString(typo.REW(q.str()), "")
		}
		return append(out, q)
	}
	return continueOrKeep(out, run, next)
}

func continueOrKeep(out, run []*elem, next string) []*elem {
	if len(run) == 0 {
		return out
	}
	prev := lastOf(out)
	if !continues(prev.Type, next) {
		return append(out, run...)
	}
	for _, e := range run {
		appendLine(prev, e.str())
	}
	return out
}

func continues(prev, next string) bool {
	switch prev {
	case "source", "leadcomment":
		return true
	case "answer", "comment", "zachet", "nezachet":
		return next != "question" && next != "answer" && next != "leadcomment"
	}
	return false
}

// questionStart is the last line of the run that opens with a number while the
// line before it does not, so that a numbered list above the question stays out.
func (p *parser) questionStart(run []*elem, prev *elem) (k int, numbered bool) {
	number := p.rx.field("number")
	for k = len(run) - 1; k >= 0; k-- {
		before := prev.str()
		if k > 0 {
			before = run[k-1].str()
		}
		if number.MatchString(typo.REW(run[k].str())) && !number.MatchString(typo.REW(before)) {
			return k, true
		}
	}
	return 0, false
}

func hasQuestion(out []*elem) bool {
	n := len(out)
	if n > 0 && out[n-1].Type == "leadcomment" {
		n--
	}
	return n > 0 && out[n-1].Type == "question"
}

func insertQuestion(out []*elem, q *elem) []*elem {
	n := len(out)
	if n > 0 && out[n-1].Type == "leadcomment" {
		return append(out[:n-1], q, out[n-1])
	}
	return append(out, q)
}

func lastOf(out []*elem) *elem {
	if len(out) == 0 {
		return &elem{}
	}
	return out[len(out)-1]
}

func appendLine(e *elem, line string) {
	if e.str() == "" {
		e.Content = line
		return
	}
	e.Content = e.str() + sep + line
}

func joinLines(run []*elem) string {
	q := &elem{}
	for _, e := range run {
		appendLine(q, e.str())
	}
	return q.str()
}

// afterCredits is where a question starts in a run that opens a tour: past the
// last line that reads like the editors' credits, if a line follows it.
func (p *parser) afterCredits(run []*elem) int {
	if p.rx.credits == nil {
		return 0
	}
	for k := len(run) - 1; k >= 0; k-- {
		if p.rx.credits.MatchString(run[k].str()) {
			if k+1 < len(run) {
				return k + 1
			}
			return 0
		}
	}
	return 0
}
