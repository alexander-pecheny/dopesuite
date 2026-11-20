package qyserver

import (
	"encoding/json"
	"html"
	"regexp"
	"strconv"
	"strings"

	"qy/qy/domain/question"
	"qy/qy/platform/markdown"
	"qy/qy/storage/store"
)

// card is a question as one page shows it. The jury's pages leave the credits
// out; the results page fills them in along with the places.
type card struct {
	qy int
	q  question.Question
	// tr is the question's Russian translation, for one written in another
	// language; the card then offers a switch between the two.
	tr         *question.Question
	tournament string
	places     string
	factcheck  string
	extra      string
}

// juryCard is a card of the jury's copy, with its translation if it has one.
func juryCard(q store.Question) card {
	c := card{qy: q.QY, q: q.Clean, extra: q.ExtraContext, factcheck: q.Factcheck}
	if tr, ok := q.JuryTranslation(); ok {
		c.tr = &tr
	}
	return c
}

// withoutFactcheck is the card before the factcheck has opened.
func (c card) withoutFactcheck() card {
	c.factcheck = ""
	return c
}

func qyLabel(n int) string { return "QY" + strconv.Itoa(n) }

// questionHTML is a card's frame: the number and the text, then the answer and
// the rest behind a disclosure. The question itself is drawn in the browser by
// chgksuite's renderer (web/ts/questions.ts), the one xy's boards and the
// nomination form's preview use, from the fields in data-question; until then,
// or without the script, the frame shows them as plain text.
func questionHTML(c card) string {
	var b strings.Builder
	anchor, label := qyLabel(c.qy), str.Question.Label(qyLabel(c.qy))
	number := anchor
	if c.qy == 0 {
		label, number = str.Question.Unnumbered(), ""
	}
	b.WriteString(`<div id="` + anchor + `" data-question="` + html.EscapeString(rendererFields(c.q)) + `"`)
	if c.tr != nil {
		b.WriteString(` data-translation="` + html.EscapeString(rendererFields(*c.tr)) + `"`)
	}
	b.WriteString(` data-number="` + number + `">`)
	if c.tr != nil {
		// The switch, which the script shows; without it the translation is the
		// disclosure further down.
		b.WriteString(`<div class="seg" role="tablist" data-part="langs" hidden>` +
			`<button type="button" class="seg-btn active" role="tab" aria-selected="true" data-view="orig">` +
			html.EscapeString(str.Question.Original()) + `</button>` +
			`<button type="button" class="seg-btn" role="tab" aria-selected="false" data-view="tr">` +
			html.EscapeString(str.Question.Translation()) + `</button></div>`)
	}
	b.WriteString(`<p data-part="question"><b>` + html.EscapeString(label) + `</b> ` + plain(c.q.Text) + "</p>")
	b.WriteString("<details><summary>" + html.EscapeString(str.Question.ShowAnswer()) + "</summary>")
	b.WriteString(`<div data-part="fields"><p>` + plain(question.FormatFields(c.q)) + "</p></div>")
	if c.tournament != "" {
		b.WriteString(`<div class="pv-field"><strong class="pv-label">` + html.EscapeString(str.Question.Tournament()) +
			" </strong>" + plain(c.tournament) + "</div>")
	}
	if c.extra != "" {
		b.WriteString(rich(c.extra))
	}
	if c.places != "" {
		b.WriteString("<p><b>" + html.EscapeString(c.places) + "</b></p>")
	}
	b.WriteString("</details>")
	if c.tr != nil {
		b.WriteString(`<details data-part="translation"><summary>` + html.EscapeString(str.Question.TranslationDisclosure()) + "</summary>")
		b.WriteString("<p>" + plain(question.Format(*c.tr)) + "</p></details>")
	}
	if c.factcheck != "" {
		b.WriteString(`<details id="` + anchor + `_factcheck"><summary>` + html.EscapeString(str.Question.Factcheck()) + "</summary>")
		b.WriteString(rich(c.factcheck) + "</details>")
	}
	b.WriteString("</div>")
	return b.String()
}

// rendererFields is a question as the browser's renderer takes it, JSON.
func rendererFields(q question.Question) string {
	handout, text := question.SplitHandout(q.Text)
	fields, _ := json.Marshal(map[string]string{
		"text": text, "handout": handout, "answer": q.Answer, "zachet": q.Zachet,
		"nezachet": q.Nezachet, "comment": q.Comment, "source": q.Source, "author": q.Author,
	})
	return string(fields)
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
