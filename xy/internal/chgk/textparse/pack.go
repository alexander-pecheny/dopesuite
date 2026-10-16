package textparse

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
	"xy/internal/chgk/fsource"
)

// flushOn are the element types that end the question being accumulated.
var flushOn = map[string]bool{
	"number": true, "tour": true, "tourrev": true,
	"question": true, "meta": true, "editor": true,
}

func (p *parser) pack() fsource.Doc {
	var final fsource.Doc
	cur := fsource.NewQuestion()

	for _, e := range p.structure {
		if flushOn[e.Type] && cur.Has("question") {
			p.creditAuthor(cur)
			final = append(final, fsource.Pair{Type: "Question", Content: cur})
			cur = fsource.NewQuestion()
		}
		if !questionLabels[e.Type] {
			final = append(final, fsource.Pair{Type: e.Type, Content: e.Content})
			continue
		}
		if !cur.Has(e.Type) {
			cur.Set(e.Type, e.Content)
			continue
		}
		// duplicate field: chgksuite warns and merges
		old := cur.Get(e.Type)
		oldList, oldIsList := old.([]any)
		newList, newIsList := e.Content.([]any)
		oldStr, oldIsStr := old.(string)
		newStr, newIsStr := e.Content.(string)
		switch {
		case newIsList && oldIsStr:
			cur.Set(e.Type, append([]any{oldStr}, newList...))
		case newIsStr && oldIsList:
			cur.Set(e.Type, append(oldList, newStr))
		case newIsList && oldIsList:
			cur.Set(e.Type, append(oldList, newList...))
		case newIsStr && oldIsStr:
			cur.Set(e.Type, oldStr+sep+newStr)
		}
	}
	if !cur.Empty() {
		p.creditAuthor(cur)
		final = append(final, fsource.Pair{Type: "Question", Content: cur})
	}
	return final
}

// creditAuthor is --defaultauthor: a question that names no author gets the
// one the whole file was parsed with.
func (p *parser) creditAuthor(q *fsource.Question) {
	if p.defaultAuthor != "" && !q.Has("author") {
		q.Set("author", p.defaultAuthor)
	}
}

var (
	reDateDMY = regexp.MustCompile(`[0-9]{2}\.[0-9]{2}\.[0-9]{4}`)
	reDateYMD = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}`)
)

// searchForDate ports search_for_date + check_date: a date-shaped string only
// counts if it's plausible (not before 1980, not more than a year in the future).
func (p *parser) searchForDate(s string) string {
	try := func(re *regexp.Regexp, layout string) string {
		for _, m := range re.FindAllString(s, -1) {
			d, err := time.Parse(layout, m)
			if err != nil {
				continue
			}
			today := p.now.Truncate(day)
			if d.Year() >= minDateYear && (d.Before(today) || d.Sub(today) <= dateHorizon) {
				return m
			}
		}
		return ""
	}
	if m := try(reDateDMY, "02.01.2006"); m != "" {
		return m
	}
	return try(reDateYMD, "2006-01-02")
}

// headerPass ports step 7: promote the leading meta line to the package heading
// and find the date among the header lines.
func (p *parser) headerPass(final fsource.Doc) fsource.Doc {
	fq := -1
	for i, pr := range final {
		if pr.Type == "Question" {
			fq = i
			break
		}
	}
	if fq < 0 {
		return final
	}
	dateDefined, headingDefined := false, false
	for _, pr := range final[:fq] {
		switch pr.Type {
		case "date":
			dateDefined = true
		case "heading", "ljheading":
			headingDefined = true
		}
	}
	if !headingDefined && len(final) > 0 && final[0].Type == "meta" {
		final[0].Type = "heading"
	}
	// a heading stays a heading; the date it contains becomes its own element
	// (pre-existing behaviour, previously via the ljheading copy that used to
	// absorb this conversion)
	makeDate := func(i int) {
		if final[i].Type == "heading" {
			final = append(final[:i], append(fsource.Doc{{Type: "date", Content: final[i].Content}}, final[i:]...)...)
		} else {
			final[i].Type = "date"
		}
	}
	for i := 0; !dateDefined && i < fq && i < len(final); i++ {
		content, ok := final[i].Content.(string)
		if !ok {
			continue
		}
		n := float64(utf8.RuneCountInString(content)) / dateShare

		if m := p.rx.field("date2").FindString(content); m != "" && float64(utf8.RuneCountInString(m)) >= n {
			makeDate(i)
			dateDefined = true
			break
		}
		if m := p.searchForDate(content); m != "" && float64(utf8.RuneCountInString(m)) >= n {
			makeDate(i)
			dateDefined = true
			break
		}
	}
	return final
}

func (p *parser) postprocessQuestion(q *fsource.Question) {
	if n, ok := q.Get("number").(string); ok && strings.TrimSpace(n) == "" {
		q.Delete("number")
	}
	if !q.Has("question") {
		return
	}
	qs := joinStrings(q.Get("question"))
	for _, prefix := range zeroPrefixes {
		if strings.HasPrefix(qs, prefix) {
			q.Set("question", replaceDeep(q.Get("question"), prefix, ""))
			q.Set("number", 0)
			qs = joinStrings(q.Get("question"))
		}
	}
	for _, k := range []string{"zachet", "nezachet", "source", "comment", "author"} {
		if !q.Has(k) {
			p.tryExtractField(q, k)
		}
	}
	qs = joinStrings(q.Get("question"))
	// "Handout:\n[…" → "[Handout:\n…" so the handout ends up inside the
	// bracketed block the composer looks for. A label that is already in a
	// bracket is a handout whose own text opens with a bracketed word, and is
	// left alone.
	if m := p.rx.handoutBefore.FindStringSubmatch(qs); m != nil {
		q.Set("question", replaceDeep(q.Get("question"), m[0], m[1]+"["+p.rx.handoutLabel+":"+m[2]))
	}
	liftLeadingImages(q)
}

// reLeadingImages matches the (img …) lines a question opens with, and the text
// that has to follow them.
var reLeadingImages = regexp.MustCompile(`(?s)\A((?:[ \t]*\(img [^)\n]*\)[ \t]*\n)+)(.*\S.*)\z`)

// liftLeadingImages makes the handout out of the pictures a question opens with.
// A picture on its own line above the question text is what the teams are given
// to look at, and in a Word document nobody had to type «Handout:» above it for
// that to be true — so an imported question kept the (img …) at the head of its
// text and its handout field stayed empty (#80). A question that is nothing but
// a picture is left alone: it would be left with no text at all.
func liftLeadingImages(q *fsource.Question) {
	if q.Has("handout") {
		return
	}
	text, ok := q.Get("question").(string)
	if !ok {
		return
	}
	m := reLeadingImages.FindStringSubmatch(text)
	if m == nil {
		return
	}
	q.Set("handout", strings.TrimSpace(m[1]))
	q.Set("question", strings.TrimSpace(m[2]))
}

// tryExtractField ports _try_extract_field: a field that never got its own line
// (e.g. a "zachet:" label buried at the end of the answer) is pulled out of whichever
// field currently holds it.
func (p *parser) tryExtractField(q *fsource.Question, k string) {
	re := p.rx.field(k)
	if re == nil {
		return
	}
	keys := append([]string(nil), q.Keys()...)
	// Python iterates sorted(question.keys())
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	for _, k1 := range keys {
		var strs []string
		collectStrings(q.Get(k1), &strs)
		for _, s := range strs {
			lines := strings.Split(s, "\n")
			for i, line := range lines {
				loc := re.FindStringIndex(line)
				if loc == nil {
					continue
				}
				matched := line[loc[0]:loc[1]]
				val := strings.Join(append([]string{strings.Replace(line, matched, "", 1)}, lines[i+1:]...), "\n")
				toErase := []string{matched, val}
				val = strings.TrimSpace(val)
				if val == "" {
					return // Python: `if val:` — an empty extraction is dropped
				}
				q.Set(k, val)
				for _, v := range toErase {
					q.Set(k1, replaceDeep(q.Get(k1), v, ""))
				}
				return
			}
		}
	}
}

func collectStrings(v any, out *[]string) {
	switch t := v.(type) {
	case string:
		*out = append(*out, t)
	case []any:
		for _, x := range t {
			collectStrings(x, out)
		}
	}
}

func joinStrings(v any) string {
	var strs []string
	collectStrings(v, &strs)
	return strings.Join(strs, "\n")
}

// replaceDeep ports ChgkParser._replace: replace and strip, recursing into lists.
func replaceDeep(v any, from, to string) any {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(strings.ReplaceAll(t, from, to))
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = replaceDeep(x, from, to)
		}
		return out
	}
	return v
}

// finish finds the package's heading and date, cleans each question, and
// spells the tours out in words if asked to.
func (p *parser) finish(final fsource.Doc, tourNumbersAsWords bool) fsource.Doc {
	final = p.headerPass(final)
	tours := 0
	for i, pr := range final {
		switch {
		case pr.Type == "Question":
			p.postprocessQuestion(pr.Content.(*fsource.Question))
		case pr.Type == "tour" && tourNumbersAsWords && tours < len(tourWords):
			final[i].Content = tourWords[tours] + tourSuffix
			tours++
		}
	}
	return final
}
