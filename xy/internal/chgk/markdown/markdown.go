// Package markdown is composer/markdown.py: a package as Markdown, or as the
// Reddit dialect of it, where the answer is a spoiler.
package markdown

import (
	"fmt"
	"strconv"
	"strings"

	xystrings "xy/i18nstrings"
	"xy/internal/chgk/fsource"
	"xy/internal/chgk/imghost"
	"xy/internal/chgk/inline"
)

// Options is the switch between the two dialects.
type Options struct {
	// Reddit is --filetype redditmd: the answer through to the author sits
	// inside a >! … !< spoiler, and a picture is a named link rather than an
	// embed, because Reddit renders no inline images.
	Reddit bool
}

// Export renders the questions of a package. images maps a picture's name, as
// the (img …) directives spell it, to its bytes; a directive that already names
// a URL is left alone, and anything else is uploaded to host.
func Export(doc fsource.Doc, images map[string][]byte, host imghost.Host, o Options) (string, error) {
	e := &exporter{images: images, host: host, opts: o, qcount: 1}
	var out []string
	for _, p := range doc {
		q, ok := p.Content.(*fsource.Question)
		if !ok || p.Type != "Question" {
			continue
		}
		s, err := e.question(q)
		if err != nil {
			return "", err
		}
		out = append(out, s)
	}
	return strings.Join(out, "\n\n"), nil
}

type exporter struct {
	images map[string][]byte
	host   imghost.Host
	opts   Options
	qcount int
}

// question is markdown_format_question.
func (e *exporter) question(q *fsource.Question) (string, error) {
	s := xystrings.Default
	if v, ok := q.Get("setcounter").(string); ok {
		if n, err := strconv.Atoi(v); err == nil {
			e.qcount = n
		}
	}
	number := strconv.Itoa(e.qcount)
	if n := q.Get("number"); n != nil {
		number = fmt.Sprintf("%v", n)
	} else {
		e.qcount++
	}

	body, err := e.yap(q.Get("question"))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("__" + s.Docs.Markdown.Question(number) + "__: " + body + "  \n")

	open, close := "", ""
	if e.opts.Reddit {
		open, close = ">!", "!<"
	}
	answer, err := e.yap(q.Get("answer"))
	if err != nil {
		return "", err
	}
	b.WriteString("__" + s.Docs.Markdown.Answer() + ":__ " + open + answer + "  \n")
	for _, f := range []struct{ field, label string }{
		{"zachet", s.Docs.Markdown.Zachet()}, {"nezachet", s.Docs.Markdown.Nezachet()},
		{"comment", s.Docs.Markdown.Comment()}, {"source", s.Docs.Markdown.Source()},
	} {
		if !q.Has(f.field) {
			continue
		}
		v, err := e.yap(q.Get(f.field))
		if err != nil {
			return "", err
		}
		b.WriteString("__" + f.label + ":__ " + v + "  \n")
	}
	if q.Has("author") {
		author, err := e.yap(q.Get("author"))
		if err != nil {
			return "", err
		}
		b.WriteString(close + "\n__" + s.Docs.Markdown.Author() + ":__ " + author + "  \n")
	} else {
		b.WriteString(close + "\n")
	}
	return b.String(), nil
}

// yap is markdownyapper: a list of lists is laid out line by line, anything
// else in one go.
func (e *exporter) yap(v any) (string, error) {
	list, ok := v.([]any)
	if !ok || !anyNested(list) {
		return e.layout(v)
	}
	parts := make([]string, len(list))
	for i, x := range list {
		s, err := e.layout(x)
		if err != nil {
			return "", err
		}
		parts[i] = s
	}
	return strings.Join(parts, "  \n"), nil
}

// layout is markdown_element_layout: a list becomes "1\. …" items, escaped so
// Markdown does not renumber them.
func (e *exporter) layout(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return e.format(t)
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			s, err := e.layout(x)
			if err != nil {
				return "", err
			}
			parts[i] = strconv.Itoa(i+1) + `\. ` + s
		}
		return strings.Join(parts, "  \n"), nil
	}
	return "", nil
}

// format is markdownformat: the inline runs, each in its Markdown spelling.
func (e *exporter) format(s string) (string, error) {
	var b strings.Builder
	for _, r := range inline.Parse4sElem(s) {
		switch r.Kind {
		case "":
			b.WriteString(r.Text)
		case "hyperlink":
			b.WriteString("<" + r.Text + ">")
		case "screen":
			b.WriteString(r.ForScreen)
		case "italic":
			b.WriteString("_" + r.Text + "_")
		case "img":
			link, err := e.link(r.Text)
			if err != nil {
				return "", err
			}
			if e.opts.Reddit {
				b.WriteString("[" + xystrings.Default.Docs.Markdown.ImageLink() + "](" + link + ")")
			} else {
				b.WriteString("![](" + link + ")")
			}
		}
	}
	out := strings.TrimRight(b.String(), "\n")
	return strings.ReplaceAll(out, "\n", "  \n"), nil
}

// link is parse_and_upload_image. chgksuite tests the whole directive for a
// URL, not the filename inside it, so "(img w=200 https://…)" is not one.
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
		// chgksuite leaves an empty link for a picture it cannot find.
		return "", nil
	}
	return e.host.Upload(name, data)
}

func anyNested(list []any) bool {
	for _, x := range list {
		if _, ok := x.([]any); ok {
			return true
		}
	}
	return false
}
