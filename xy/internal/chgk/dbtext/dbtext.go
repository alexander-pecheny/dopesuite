// Package dbtext is composer/db.py: a package in the plain text db.chgk.info
// takes as a submission. It is the other side of textparse.ParseDB.
package dbtext

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/imghost"
	"xy/internal/chgk/inline"
)

// Options is what --remove_accents asks for.
type Options struct {
	// RemoveAccents turns a stressed vowel into a capital one, which is how the
	// base spells stress.
	RemoveAccents bool
}

// baseMapping is DbExporter.BASE_MAPPING: the header each element is filed under.
var baseMapping = map[string]string{
	"section": "Тур", "heading": "Чемпионат", "editor": "Редактор", "meta": "Инфо",
}

var (
	reEditors  = regexp.MustCompile(`^[рР]едакторы? *(пакета|тура)? *[—\-–−:] ?`)
	reDateSep  = regexp.MustCompile(` [—–-] `)
	reAccented = regexp.MustCompile(`(.)` + "́")
)

// Export renders a package as the base's plain text. images maps a picture's
// name to its bytes; anything not already a URL is published through host.
func Export(doc fsource.Doc, images map[string][]byte, host imghost.Host, o Options) (string, error) {
	e := &exporter{images: images, host: host, opts: o}
	doc = hoistWarmups(doc)

	var out strings.Builder
	for _, p := range doc {
		if q, ok := p.Content.(*fsource.Question); ok && p.Type == "Question" {
			foldNezachet(e, q)
		}
		if p.Type == "editor" {
			if s, ok := p.Content.(string); ok {
				p.Content = reEditors.ReplaceAllString(s, "")
			}
		}
		s, err := e.element(p)
		if err != nil {
			return "", err
		}
		out.WriteString(s)
	}
	return out.String(), nil
}

// hoistWarmups is the first half of DbExporter.export: consecutive «Инфо» blocks
// are merged, and a warm-up question is moved to the top of its tour under a
// «Нулевой вопрос N» heading of its own. chgksuite walks the structure while
// rearranging it, so this does too.
func hoistWarmups(doc fsource.Doc) fsource.Doc {
	d := append(fsource.Doc(nil), doc...)
	lastTour, zeroq := 0, 1
	for i := 0; i < len(d); i++ {
		if d[i].Type == "section" {
			lastTour = i
		}
		for d[i].Type == "meta" && i+1 < len(d) && d[i+1].Type == "meta" {
			d[i].Content = text(d[i].Content) + "\n" + text(d[i+1].Content)
			d = append(d[:i+1], d[i+2:]...)
		}
		q, isQuestion := d[i].Content.(*fsource.Question)
		if d[i].Type != "Question" || !isQuestion || !fsource.IsWarmup(q) {
			continue
		}
		q.Set("number", 1)
		pair := d[i]
		d = append(d[:i], d[i+1:]...)
		d = append(d[:lastTour], append(fsource.Doc{
			{Type: "section", Content: fmt.Sprintf("Нулевой вопрос %d", zeroq)}, pair,
		}, d[lastTour:]...)...)
		zeroq++
	}
	return d
}

// foldNezachet moves a «незачёт» onto the end of the зачёт (or of the answer):
// the base has no field for it.
func foldNezachet(e *exporter, q *fsource.Question) {
	if !q.Has("nezachet") {
		return
	}
	field := "answer"
	if q.Has("zachet") {
		field = "zachet"
	}
	nezachet, err := e.yap(q.Get("nezachet"))
	if err != nil {
		return
	}
	q.Delete("nezachet")

	last := lastValue(q.Get(field))
	dot := "."
	if strings.HasSuffix(last, ".") {
		dot = ""
	}
	setLastValue(q, field, last+dot+"\n   Незачёт: "+nezachet)
}

func lastValue(v any) string {
	if list, ok := v.([]any); ok && len(list) > 0 {
		return text(list[len(list)-1])
	}
	return text(v)
}

func setLastValue(q *fsource.Question, field, value string) {
	if list, ok := q.Get(field).([]any); ok && len(list) > 0 {
		list[len(list)-1] = value
		return
	}
	q.Set(field, value)
}

type exporter struct {
	images map[string][]byte
	host   imghost.Host
	opts   Options
	qcount int
}

// element is base_format_element: everything the base has a header for, and
// nothing else.
func (e *exporter) element(p fsource.Pair) (string, error) {
	if q, ok := p.Content.(*fsource.Question); ok && p.Type == "Question" {
		return e.question(q)
	}
	if header, ok := baseMapping[p.Type]; ok {
		v, err := e.yap(p.Content)
		if err != nil {
			return "", err
		}
		return header + ":\n" + v + "\n\n", nil
	}
	if p.Type == "date" {
		s := text(p.Content)
		if sep := reDateSep.FindString(s); sep != "" {
			parts := strings.Split(s, sep)
			return "Дата:\n" + wrapDate(parts[0]) + " - " + wrapDate(parts[len(parts)-1]) + "\n\n", nil
		}
		return "Дата:\n" + wrapDate(s) + "\n\n", nil
	}
	return "", nil
}

// question is base_format_question.
func (e *exporter) question(q *fsource.Question) (string, error) {
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
	b.WriteString("Вопрос " + number + ":\n" + body + "\n\n")
	for _, f := range []struct{ field, header string }{
		{"answer", "Ответ"}, {"zachet", "Зачет"}, {"nezachet", "Незачет"},
		{"comment", "Комментарий"}, {"source", "Источник"}, {"author", "Автор"},
	} {
		if f.field != "answer" && !q.Has(f.field) {
			continue
		}
		// chgksuite prints the зачёт under the «Незачет» header too. Unreachable,
		// since the незачёт is folded away before this runs, but faithfully so.
		from := f.field
		if from == "nezachet" {
			from = "zachet"
		}
		v, err := e.yap(q.Get(from))
		if err != nil {
			return "", err
		}
		b.WriteString(f.header + ":\n" + v + "\n\n")
	}
	return b.String(), nil
}

// yap is baseyapper: a list of lists is laid out line by line, anything else in
// one go.
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
	return strings.Join(parts, "\n"), nil
}

// layout is base_element_layout: a list becomes the base's own "   1. " items.
func (e *exporter) layout(v any) (string, error) {
	switch t := v.(type) {
	case string:
		if e.opts.RemoveAccents {
			t = reAccented.ReplaceAllStringFunc(t, func(m string) string {
				return strings.ToUpper(string([]rune(m)[0]))
			})
		}
		return e.format(t)
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			s, err := e.layout(x)
			if err != nil {
				return "", err
			}
			parts[i] = fmt.Sprintf("   %d. %s", i+1, s)
		}
		return strings.Join(parts, "\n"), nil
	}
	return "", nil
}

// format is baseformat: the base indents a wrapped line by three spaces, which
// is the same three its reader strips.
func (e *exporter) format(s string) (string, error) {
	var b strings.Builder
	for _, r := range inline.Parse4sElem(s) {
		switch r.Kind {
		case "", "hyperlink":
			b.WriteString(strings.ReplaceAll(r.Text, "\n", "\n   "))
		case "italic":
			b.WriteString(r.Text)
		case "screen":
			b.WriteString(r.ForPrint)
		case "img":
			link, err := e.link(r.Text)
			if err != nil {
				return "", err
			}
			b.WriteString("(pic: " + link + ")")
		}
	}
	return inline.ReplaceEscaped(strings.TrimRight(b.String(), "\n")), nil
}

func (e *exporter) link(arg string) (string, error) {
	if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
		return arg, nil
	}
	img, ok := inline.ParseImg(arg)
	if !ok {
		return "", nil
	}
	data, found := e.images[img.Name]
	if !found || e.host == nil {
		return "", nil
	}
	// The base takes a picture at the size the directive asks for, so a sized
	// one is published resized — under the name chgksuite gives the copy.
	name := img.Name
	if img.Width > 0 || img.Height > 0 {
		resized, err := resize(data, img.Width, img.Height)
		if err != nil {
			return "", err
		}
		if resized != nil {
			data, name = resized, trimExt(img.Name)+"_resized.png"
		}
	}
	return e.host.Upload(name, data)
}

func trimExt(name string) string {
	if i := strings.LastIndex(name, "."); i > 0 {
		return name[:i]
	}
	return name
}

func anyNested(list []any) bool {
	for _, x := range list {
		if _, ok := x.([]any); ok {
			return true
		}
	}
	return false
}

func text(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
