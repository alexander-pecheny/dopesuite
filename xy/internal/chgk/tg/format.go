// Package tg is chgksuite's `compose telegram`: a parsed package, posted to a
// Telegram channel one question at a time, with the answers folded away and the
// discussion group carrying the replies.
//
// chgksuite has two renderings of a question, and only one of them is alive:
// rich_mode is set unconditionally in its constructor, so every message goes out
// as a sendRichMessage payload (HTML with <p>/<details>/<footer>/<img>) and the
// older path — plain HTML split into 4096-character messages, photos posted
// separately — is unreachable. This port follows the live one.
package tg

import (
	"fmt"
	"regexp"
	"strings"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/i18n"
	"xy/internal/chgk/inline"
)

// Options are the `compose telegram` switches that change what is written.
type Options struct {
	// NoSpoilers prints the answer openly instead of behind a <details>.
	NoSpoilers bool
	// DisableAsterisks leaves "*" alone instead of escaping it to &#42;.
	DisableAsterisks bool
	// SkipUntil starts the export at that question number (0 = from the top).
	SkipUntil int
	// Language is --language: which labels_*.toml the headings come from.
	// Empty is Russian.
	Language string
}

const richImgHeightP = 200

// imgSentinel marks an image inside a message's text until the payload is
// finalized and the picture becomes an <img> and an upload.
const imgSentinel = "\x00"

var reImgSentinel = regexp.MustCompile("\x00img:([^\x00]+)\x00")

// formatter renders one package's text. images maps a picture's name, as the
// (img …) directives spell it, to its bytes.
type formatter struct {
	opts   Options
	images map[string][]byte
	labels i18n.Labels
}

// replaceChars is tg_replace_chars: what a run of plain text may not carry into
// Telegram's HTML.
func (f *formatter) replaceChars(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	if !f.opts.DisableAsterisks {
		s = strings.ReplaceAll(s, "*", "&#42;")
	}
	s = strings.ReplaceAll(s, "_", "&#95;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return strings.ReplaceAll(s, "<", "&lt;")
}

// hyperlink is _format_html_hyperlink: the URL is both the target and the text.
func (f *formatter) hyperlink(url string) string {
	href := escapeHTML(inline.URLQuote(url), true)
	text := strings.ReplaceAll(escapeHTML(url, false), "_", "&#95;")
	if !f.opts.DisableAsterisks {
		text = strings.ReplaceAll(text, "*", "&#42;")
	}
	return `<a href="` + href + `">` + text + `</a>`
}

// escapeHTML is python's html.escape: &, < and > always; quotes only when asked.
func escapeHTML(s string, quote bool) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	if quote {
		s = strings.ReplaceAll(s, `"`, "&quot;")
		s = strings.ReplaceAll(s, "'", "&#x27;")
	}
	return s
}

// format is tgformat: one 4s string into Telegram HTML, with each picture left
// as a sentinel for the payload to resolve.
func (f *formatter) format(s string) (string, error) {
	var b strings.Builder
	for _, r := range inline.Parse4sElem(s) {
		switch {
		case r.Kind == "":
			b.WriteString(f.replaceChars(r.Text))
		case r.Kind == "hyperlink":
			b.WriteString(f.hyperlink(r.Text))
		case r.Kind == "screen":
			b.WriteString(f.replaceChars(r.ForScreen))
		case r.Kind == "strike":
			b.WriteString("<s>" + f.replaceChars(r.Text) + "</s>")
		case strings.Contains(r.Kind, "italic"), strings.Contains(r.Kind, "bold"),
			strings.Contains(r.Kind, "underline"):
			chunk := f.replaceChars(r.Text)
			if strings.Contains(r.Kind, "italic") {
				chunk = "<i>" + chunk + "</i>"
			}
			if strings.Contains(r.Kind, "bold") {
				chunk = "<b>" + chunk + "</b>"
			}
			if strings.Contains(r.Kind, "underline") {
				chunk = "<u>" + chunk + "</u>"
			}
			b.WriteString(chunk)
		case r.Kind == "linebreak":
			b.WriteString("\n")
		case r.Kind == "img":
			if strings.HasPrefix(r.Text, "http://") || strings.HasPrefix(r.Text, "https://") {
				b.WriteString(r.Text)
				continue
			}
			im, ok := inline.ParseImg(r.Text)
			if !ok {
				return "", fmt.Errorf("bad image directive %q", r.Text)
			}
			if _, ok := f.images[im.Name]; !ok {
				return "", fmt.Errorf("image %s doesn't exist", im.Name)
			}
			b.WriteString(imgSentinel + "img:" + im.Name + imgSentinel)
		default:
			return "", fmt.Errorf("unsupported tag `%s` in telegram export", r.Kind)
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// layout is tg_element_layout: a field is either a string or a numbered list.
func (f *formatter) layout(v any) (string, error) {
	switch val := v.(type) {
	case string:
		return f.format(val)
	case []any:
		var items []string
		for i, x := range val {
			s, err := f.layout(x)
			if err != nil {
				return "", err
			}
			items = append(items, fmt.Sprintf("%d. %s", i+1, s))
		}
		return strings.Join(items, "\n"), nil
	}
	return "", nil
}

// value is tgyapper: a field carrying a preamble and a list renders the
// preamble as its own line above the numbered items.
func (f *formatter) value(v any) (string, error) {
	val, ok := v.([]any)
	if !ok {
		return f.layout(v)
	}
	nested := false
	for _, x := range val {
		if _, ok := x.([]any); ok {
			nested = true
		}
	}
	if !nested {
		return f.layout(val)
	}
	var parts []string
	for _, x := range val {
		s, err := f.layout(x)
		if err != nil {
			return "", err
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "\n"), nil
}

// questionParts is _format_question_parts: every labelled field, rendered.
type questionParts struct {
	question, answer, zachet, nezachet, comment, source, author string
}

func (f *formatter) questionParts(q *fsource.Question, number string) (questionParts, error) {
	var p questionParts
	field := func(name string) (string, error) {
		v := q.Get(name)
		if v == nil {
			return "", nil
		}
		s, err := f.value(v)
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		return "<b>" + f.label(q, name) + ":</b> " + s, nil
	}
	txt, err := f.value(q.Get("question"))
	if err != nil {
		return p, err
	}
	// The two trailing spaces are chgksuite's; _rich_render strips them.
	p.question = "<b>" + f.label(q, "question") + " " + number + ":</b> " + txt + "  \n"
	for _, spec := range []struct {
		name string
		dst  *string
	}{
		{"answer", &p.answer}, {"zachet", &p.zachet}, {"nezachet", &p.nezachet},
		{"comment", &p.comment}, {"source", &p.source}, {"author", &p.author},
	} {
		s, err := field(spec.name)
		if err != nil {
			return p, err
		}
		*spec.dst = s
	}
	return p, nil
}

// label honours a question's own !!Label override, and the plural "Источники".
func (f *formatter) label(q *fsource.Question, field string) string {
	if ov, ok := q.Get("overrides").(map[string]string); ok {
		if v, ok := ov[field]; ok {
			return v
		}
	}
	if field == "source" {
		if _, isList := q.Get("source").([]any); isList {
			return f.labels.Field("sources")
		}
	}
	return f.labels.Field(field)
}

// handoutImgRe matches the "[Раздаточный материал: <picture>]" wrapper: in a
// message that embeds the picture itself, the wrapper says nothing.
func handoutImgRe(labels i18n.Labels) *regexp.Regexp {
	return regexp.MustCompile(`\[` + regexp.QuoteMeta(labels.Field("handout")) +
		`:\s*(\x00img:[^\x00]+\x00)\s*\]`)
}

// question renders one question as the HTML of a single rich message: the text
// and its handout in the open, everything else folded into a <details>, sources
// and authors in a <footer>.
func (f *formatter) question(q *fsource.Question, number string) (string, error) {
	p, err := f.questionParts(q, number)
	if err != nil {
		return "", err
	}
	html := richRender(strings.TrimSpace(handoutImgRe(f.labels).ReplaceAllString(p.question, "$1")))

	body := richRender(joinNonEmpty("\n", p.answer, p.zachet, p.nezachet, p.comment))
	if small := joinNonEmpty("\n", p.source, p.author); small != "" {
		// A <footer> holds text only, so any picture in it moves after it.
		imgs := reImgSentinel.FindAllString(small, -1)
		small = reImgSentinel.ReplaceAllString(small, "")
		body += "<footer>" + richBr(strings.TrimSpace(small)) + "</footer>"
		body += strings.Join(imgs, "")
	}
	if body != "" {
		if f.opts.NoSpoilers {
			html += body
		} else {
			html += "<details><summary>" + f.labels.Field("answer") + "</summary>" + body + "</details>"
		}
	}
	return html, nil
}

func joinNonEmpty(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

func richBr(s string) string { return strings.ReplaceAll(s, "\n", "<br/>") }

// richRender wraps text in <p> blocks, leaving the image sentinels between them.
func richRender(text string) string {
	var b strings.Builder
	pos := 0
	for _, m := range reImgSentinel.FindAllStringIndex(text, -1) {
		if seg := strings.TrimSpace(text[pos:m[0]]); seg != "" {
			b.WriteString("<p>" + richBr(seg) + "</p>")
		}
		b.WriteString(text[m[0]:m[1]])
		pos = m[1]
	}
	if seg := strings.TrimSpace(text[pos:]); seg != "" {
		b.WriteString("<p>" + richBr(seg) + "</p>")
	}
	return b.String()
}

// heading is _wrap_heading: a heading is its own block, not a bold line.
func heading(text string) string { return "<h3>" + text + "</h3>" }

// Media is one picture a rich message carries: the id its <img> refers to, and
// the picture itself.
type Media struct {
	ID   string
	Name string
	Data []byte
}

// finalize is _finalize_rich: sentinels become <img> tags and a list of uploads.
func (f *formatter) finalize(html string) (string, []Media) {
	var media []Media
	out := reImgSentinel.ReplaceAllStringFunc(html, func(m string) string {
		name := reImgSentinel.FindStringSubmatch(m)[1]
		id := fmt.Sprintf("img%d", len(media))
		media = append(media, Media{ID: id, Name: name, Data: f.images[name]})
		return `<img src="tg://photo?id=` + id + `"/>`
	})
	return out, media
}

// buffered is _split_to_messages_rich: the headings and stray text collected
// between questions, rendered as one message. A fragment that already carries
// block tags (a heading) goes in as it is.
func (f *formatter) buffered(texts []string) string {
	var b strings.Builder
	for _, t := range texts {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "<h") || strings.Contains(t, "</p>") || strings.Contains(t, "<details>") {
			b.WriteString(t)
			continue
		}
		b.WriteString(richRender(t))
	}
	return b.String()
}
