package qyserver

import (
	"html"
	"regexp"
	"strconv"
	"strings"

	"qy/qy/domain/question"
	"qy/qy/platform/markdown"
)

// card is a question as one page shows it. The jury's pages leave the credits
// out; the results page fills them in along with the places.
type card struct {
	qy         int
	q          question.Question
	tournament string
	places     string
	factcheck  string
	extra      string
}

func qyLabel(n int) string { return "QY" + strconv.Itoa(n) }

// questionHTML renders a card the way the old static site did: the number and
// the text, then the answer and the rest behind a disclosure.
func questionHTML(c card) string {
	var b strings.Builder
	anchor, label := qyLabel(c.qy), str.Question.Label(qyLabel(c.qy))
	if c.qy == 0 {
		label = str.Question.Unnumbered()
	}
	b.WriteString(`<p><b id="` + anchor + `">` + html.EscapeString(label) + `</b> `)
	b.WriteString(plain(c.q.Text) + "</p>")
	b.WriteString("<details><summary>" + html.EscapeString(str.Question.ShowAnswer()) + "</summary>")
	for _, f := range []struct{ label, value string }{
		{str.Question.Answer(), c.q.Answer},
		{str.Question.Zachet(), c.q.Zachet},
		{str.Question.Nezachet(), c.q.Nezachet},
		{str.Question.Comment(), c.q.Comment},
		{str.Question.Source(), c.q.Source},
		{str.Question.Author(), c.q.Author},
		{str.Question.Tournament(), c.tournament},
	} {
		if f.value != "" {
			b.WriteString("<p><b>" + html.EscapeString(f.label) + "</b> " + plain(f.value) + "</p>")
		}
	}
	if c.extra != "" {
		b.WriteString(rich(c.extra))
	}
	if c.places != "" {
		b.WriteString("<p><b>" + html.EscapeString(c.places) + "</b></p>")
	}
	b.WriteString("</details>")
	if c.factcheck != "" {
		b.WriteString(`<details id="` + anchor + `_factcheck"><summary>` + html.EscapeString(str.Question.Factcheck()) + "</summary>")
		b.WriteString(rich(c.factcheck) + "</details>")
	}
	return b.String()
}

var urlRe = regexp.MustCompile(`https?://[^\s<>"]+`)

// plain is a field's text as HTML: escaped, with its line breaks kept and its
// links made clickable.
func plain(s string) string {
	esc := html.EscapeString(s)
	esc = urlRe.ReplaceAllStringFunc(esc, func(u string) string {
		return `<a href="` + u + `" rel="noopener nofollow">` + u + `</a>`
	})
	return strings.ReplaceAll(esc, "\n", "<br>")
}

// rich is admin-written Markdown (a factcheck, an extra context) as HTML.
func rich(src string) string {
	out, err := markdown.Render(src)
	if err != nil {
		return "<p>" + plain(src) + "</p>"
	}
	return out
}
