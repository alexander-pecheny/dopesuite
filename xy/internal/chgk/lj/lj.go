// Package lj is composer/lj.py's rendering half: a package as the HTML a
// LiveJournal post is written in — one post per tour when asked, the answers
// folded into <lj-spoiler>, and a navigation line linking the tours together.
//
// The posting half (XML-RPC challenge/response against livejournal.com) is
// post.go.
package lj

import (
	"bytes"
	"fmt"
	"image"
	"regexp"
	"strconv"
	"strings"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/i18n"
	"xy/internal/chgk/imghost"
	"xy/internal/chgk/inline"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// Options are the switches `compose lj` takes that shape the HTML.
type Options struct {
	// NoSpoilers prints the answers openly instead of inside <lj-spoiler>.
	NoSpoilers bool
	// SplitTours writes a post per tour rather than one for the package.
	SplitTours bool
	// GeneralImpressions adds the «Общие впечатления» post at the end.
	GeneralImpressions bool
	// Navigation adds the line linking each tour's post to the others. It needs
	// the posts' URLs, so it only means anything once they exist.
	Navigation bool
	// Language and LabelsFile pick the labels, as everywhere else.
	Language, LabelsFile string
	// NoBreak is --replace_no_break_spaces / --replace_no_break_hyphens.
	NoBreak inline.NoBreak
}

// Post is one thing to publish: the subject line and the HTML body. The first
// of a tour's list is the post; the rest are comments on it.
type Post struct {
	Header  string
	Content string
}

// Render turns a package into the posts it becomes: one list per tour under
// --splittours, otherwise a single list.
func Render(doc fsource.Doc, images map[string][]byte, host imghost.Host, o Options) ([][]Post, error) {
	labels, err := i18n.LabelsFor(o.Language, o.LabelsFile)
	if err != nil {
		return nil, err
	}
	e := &exporter{images: images, host: host, opts: o, labels: labels, counter: 1}
	if !o.SplitTours {
		posts, err := e.process(doc)
		if err != nil {
			return nil, err
		}
		return [][]Post{posts}, nil
	}
	var out [][]Post
	for _, tour := range e.splitIntoTours(doc) {
		posts, err := e.process(tour)
		if err != nil {
			return nil, err
		}
		out = append(out, posts)
	}
	return out, nil
}

type exporter struct {
	images  map[string][]byte
	host    imghost.Host
	opts    Options
	labels  i18n.Labels
	counter int
}

// splitIntoTours is split_into_tours: the structure cut at each «## тур», with
// the first tour's heading extended by its own name and the rest given one.
func (e *exporter) splitIntoTours(doc fsource.Doc) []fsource.Doc {
	var result []fsource.Doc
	var current fsource.Doc
	mode := "meta"
	for _, el := range doc {
		switch {
		case el.Type == "Question":
			mode = "questions"
			current = append(current, el)
		case mode == "meta":
			current = append(current, el)
		case el.Type == "section":
			result = append(result, current)
			current = fsource.Doc{el}
			mode = "meta"
		default:
			current = append(current, el)
		}
	}
	result = append(result, current)
	if len(result) == 0 {
		return result
	}

	headIdx, head := findHeading(result[0])
	if headIdx < 0 {
		return result
	}
	globalHeading := str(head.Content)
	globalSep := "."
	if strings.HasSuffix(globalHeading, ".") {
		globalSep = ""
	}
	if _, tour := findTour(result[0]); tour != nil {
		sep := "."
		if strings.HasSuffix(str(result[0][headIdx].Content), ".") {
			sep = ""
		}
		result[0][headIdx].Content = str(result[0][headIdx].Content) + sep + " " + str(tour.Content)
	}
	for i, tour := range result[1:] {
		if idx, _ := findHeading(tour); idx >= 0 {
			continue
		}
		name := ""
		if _, t := findTour(tour); t != nil {
			name = str(t.Content)
		}
		result[i+1] = append(fsource.Doc{{Type: "ljheading", Content: globalHeading + globalSep + " " + name}}, tour...)
	}
	if e.opts.GeneralImpressions {
		result = append(result, fsource.Doc{
			{Type: "ljheading", Content: globalHeading + globalSep + " " + e.labels.Text("general_impressions_caption")},
			{Type: "meta", Content: e.labels.Text("general_impressions_text")},
		})
	}
	return result
}

// findHeading is chgksuite_parser.find_heading: an ljheading wins outright, and
// failing that the last plain heading.
func findHeading(doc fsource.Doc) (int, *fsource.Pair) {
	last := -1
	for i := range doc {
		if doc[i].Type == "ljheading" {
			return i, &doc[i]
		}
		if doc[i].Type == "heading" {
			last = i
		}
	}
	if last >= 0 {
		return last, &doc[last]
	}
	return -1, nil
}

func findTour(doc fsource.Doc) (int, *fsource.Pair) {
	for i := range doc {
		if doc[i].Type == "section" {
			return i, &doc[i]
		}
	}
	return -1, nil
}

// process is lj_process: everything before the first question becomes the
// post's own body, and each question a comment on it.
func (e *exporter) process(doc fsource.Doc) ([]Post, error) {
	posts := []Post{{}}
	heading, ljHeading := "", ""
	i := 0
	for ; i < len(doc) && doc[i].Type != "Question"; i++ {
		text, err := e.yap(doc[i].Content, true)
		if err != nil {
			return nil, err
		}
		switch doc[i].Type {
		case "heading":
			posts[0].Content += "<center>" + text + "</center>"
			heading = text
		case "ljheading":
			ljHeading = text
		case "date", "editor":
			posts[0].Content += "\n<center>" + text + "</center>"
		case "meta":
			posts[0].Content += "\n" + text
		}
	}
	posts[0].Header = heading
	if ljHeading != "" {
		posts[0].Header = ljHeading
	}

	for _, el := range doc[i:] {
		switch el.Type {
		case "Question":
			q, ok := el.Content.(*fsource.Question)
			if !ok {
				continue
			}
			header := e.questionLabel(q)
			content, err := e.question(q)
			if err != nil {
				return nil, err
			}
			posts = append(posts, Post{Header: header, Content: content})
			e.counter++
		case "meta":
			text, err := e.yap(el.Content, true)
			if err != nil {
				return nil, err
			}
			posts = append(posts, Post{Content: text})
		}
	}
	if posts[0].Content == "" {
		posts[0].Content = e.labels.Text("general_impressions_text")
	}
	return posts, nil
}

// question is html_format_question.
func (e *exporter) question(q *fsource.Question) (string, error) {
	if v, ok := q.Get("setcounter").(string); ok {
		if n, err := strconv.Atoi(v); err == nil {
			e.counter = n
		}
	}
	body, err := e.yap(q.Get("question"), true)
	if err != nil {
		return "", err
	}
	spoilerOpen, spoilerClose := "\n<lj-spoiler>", "</lj-spoiler>"
	if e.opts.NoSpoilers {
		spoilerOpen, spoilerClose = "", ""
	}
	res := "<strong>" + e.questionLabel(q) + ".</strong> " + body + spoilerOpen
	if !q.Has("number") {
		e.counter++
	}
	for _, field := range []string{"answer", "zachet", "nezachet", "comment", "source", "author"} {
		if !q.Has(field) {
			continue
		}
		text, err := e.yap(q.Get(field), field != "source")
		if err != nil {
			return "", err
		}
		res += "\n<strong>" + e.label(q, field) + ": </strong>" + text
	}
	return res + spoilerClose, nil
}

func (e *exporter) questionLabel(q *fsource.Question) string {
	number := strconv.Itoa(e.counter)
	if n := q.Get("number"); n != nil {
		number = fmt.Sprintf("%v", n)
	}
	return i18n.QuestionLabel(e.label(q, "question"), number, e.opts.Language)
}

// label is get_label: a question's own !!Label override, and the plural when
// the field names more than one thing.
func (e *exporter) label(q *fsource.Question, field string) string {
	if ov, ok := q.Get("overrides").(map[string]string); ok {
		if v, ok := ov[field]; ok && v != "" {
			return v
		}
	}
	if field == "source" {
		if _, isList := q.Get("source").([]any); isList {
			return e.labels.Field("sources")
		}
	}
	return e.labels.Field(field)
}

// yap is htmlyapper: a list of lists is laid out line by line, anything else in
// one go.
func (e *exporter) yap(v any, nbsp bool) (string, error) {
	list, ok := v.([]any)
	if !ok || !anyNested(list) {
		return e.layout(v, nbsp)
	}
	parts := make([]string, len(list))
	for i, x := range list {
		s, err := e.layout(x, nbsp)
		if err != nil {
			return "", err
		}
		parts[i] = s
	}
	return strings.Join(parts, "\n"), nil
}

// layout is html_element_layout: a list becomes "1. …" items.
func (e *exporter) layout(v any, nbsp bool) (string, error) {
	switch t := v.(type) {
	case string:
		return e.format(t, nbsp)
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			s, err := e.layout(x, nbsp)
			if err != nil {
				return "", err
			}
			parts[i] = strconv.Itoa(i+1) + ". " + s
		}
		return strings.Join(parts, "\n"), nil
	}
	return "", nil
}

// format is htmlformat: the inline runs, each in its HTML spelling.
func (e *exporter) format(s string, nbsp bool) (string, error) {
	var b strings.Builder
	for _, r := range inline.Parse4sElem(s) {
		switch r.Kind {
		case "screen":
			b.WriteString(htmlRepl(r.ForScreen))
		case "pagebreak":
		case "strike":
			b.WriteString("<s>" + htmlRepl(r.Text) + "</s>")
		case "bold":
			b.WriteString("<b>" + htmlRepl(r.Text) + "</b>")
		case "underline":
			b.WriteString("<u>" + htmlRepl(r.Text) + "</u>")
		case "italic":
			b.WriteString("<em>" + htmlRepl(r.Text) + "</em>")
		case "linebreak":
			b.WriteString("<br>")
		case "img":
			img, err := e.image(r.Text)
			if err != nil {
				return "", err
			}
			b.WriteString(img)
		default:
			b.WriteString(htmlRepl(r.Text))
		}
	}
	out := b.String()
	if nbsp {
		out = inline.ReplaceNoBreak(out, e.opts.NoBreak)
	}
	return out, nil
}

// image uploads the picture and writes the <img>, at the size parseimg gives it:
// its own when the directive names none.
func (e *exporter) image(arg string) (string, error) {
	im, ok := inline.ParseImg(arg)
	if !ok {
		return "", nil
	}
	src, data := im.Name, e.images[im.Name]
	w, h, native := im.SizePixels(imageSize(data))
	if data != nil && e.host != nil {
		link, err := e.host.Upload(im.Name, data)
		if err != nil {
			return "", err
		}
		src = link
	}
	width, height := "", ""
	if w != -1 {
		width = " width=" + pyNum(w, native)
	}
	if h != -1 {
		height = " height=" + pyNum(h, native)
	}
	return fmt.Sprintf(`<img%s%s src="%s"/>`, width, height, src), nil
}

// imageSize reads the picture's pixel dimensions; 0, 0 when it cannot be read,
// which is what a missing file comes to.
func imageSize(data []byte) (int, int) {
	if len(data) == 0 {
		return 0, 0
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

var (
	reLowercase = regexp.MustCompile(`[а-яё]`)
	reUppercase = regexp.MustCompile(`[А-ЯЁ]`)
)

// htmlRepl is htmlrepl: the three XML escapes, then the backtick stress marks,
// which become the combining acute as an entity.
func htmlRepl(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	for {
		r := []rune(s)
		i := indexRune(r, '`')
		if i < 0 {
			break
		}
		if i+1 >= len(r) {
			s = string(append(r[:i], r[i+1:]...))
			continue
		}
		letter := string(r[i+1])
		if reLowercase.MatchString(letter) || reUppercase.MatchString(letter) {
			out := append([]rune{}, r[:i+1]...)
			out = append(out, r[i+1])
			out = append(out, []rune("&#x0301;")...)
			out = append(out, r[i+2:]...)
			r = out
		}
		s = string(append(append([]rune{}, r[:i]...), r[i+1:]...))
	}
	return s
}

func indexRune(r []rune, c rune) int {
	for i, x := range r {
		if x == c {
			return i
		}
	}
	return -1
}

// Navigation is generate_navigation: for each post, the tours' names with every
// other one linked. urls must be in the same order as the posts.
func Navigation(posts [][]Post, urls []string) []string {
	titles := make([]string, len(posts))
	for i, p := range posts {
		parts := strings.Split(p[0].Header, ". ")
		titles[i] = parts[len(parts)-1]
	}
	out := make([]string, len(titles))
	for i := range titles {
		inner := make([]string, len(urls))
		for j := range urls {
			if j == i {
				inner[j] = titles[j]
				continue
			}
			inner[j] = fmt.Sprintf(`<a href="%s">%s</a>`, urls[j], titles[j])
		}
		out[i] = strings.Join(inner, " | ")
	}
	return out
}

func anyNested(list []any) bool {
	for _, x := range list {
		if _, ok := x.([]any); ok {
			return true
		}
	}
	return false
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// pyNum writes a dimension the way Python's str() writes what parseimg
// produced: an int when the picture's own size was used, a float otherwise.
func pyNum(f float64, native bool) string {
	if native {
		return strconv.Itoa(int(f))
	}
	s := strconv.FormatFloat(f, 'g', 17, 64)
	if short := strconv.FormatFloat(f, 'g', -1, 64); mustParse(short) == f {
		s = short
	}
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

func mustParse(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
