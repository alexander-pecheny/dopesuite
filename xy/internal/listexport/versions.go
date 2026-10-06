package listexport

import (
	"strconv"
	"strings"
	"unicode"

	xystrings "xy/i18nstrings"

	"xy/internal/chgk/inline"
)

// The export half of the Version algebra (versions.ts composeVersions, ADR-0007):
// a Card's Versions folded back into one numbered question.

const pagebreak = "(PAGEBREAK)"

// fields is one question's structured form (chgk.ts CardFields, without the
// handout's value: every Version keeps its own bracket inside its question). A
// nil pointer is an absent field; a pointer to "" is a field present with no
// value, written as its bare marker.
type fields struct {
	preMarkup   *string
	hasHandout  bool
	question    *string
	answer      *string
	zachet      *string
	nezachet    *string
	comment     *string
	sources     []string // nil = absent
	authors     []string // nil = absent
	authorLabel string
	extra       *string
}

func strptr(s string) *string { return &s }

// hasInlineHandout reports whether question text holds a handout bracket,
// which splitFields lifts into the handout field (chgk.ts extractInlineHandout).
func hasInlineHandout(q string) bool {
	for _, sp := range inline.BracketSpans(q) {
		if inline.IsHandoutBody(sp.Body) {
			return true
		}
	}
	return false
}

func splitFields(desc string) fields {
	var f fields
	var pre, extra, authors []string
	seenQuestion, sawAuthor := false, false
	for _, b := range parseBlocks(desc) {
		switch {
		case b.kind == "handout" && !f.hasHandout:
			// A legacy "> " block: read, so it takes the handout's place.
			f.hasHandout = true
		case (b.kind == "question" || b.kind == "pre") && !seenQuestion:
			if !f.hasHandout && hasInlineHandout(b.text) {
				f.hasHandout = true
			}
			f.question = strptr(b.text)
			seenQuestion = true
		case b.kind == "answer" && f.answer == nil:
			f.answer = strptr(b.text)
		case b.kind == "zachet" && f.zachet == nil:
			f.zachet = strptr(b.text)
		case b.kind == "nezachet" && f.nezachet == nil:
			f.nezachet = strptr(b.text)
		case b.kind == "comment" && f.comment == nil:
			f.comment = strptr(b.text)
		case b.kind == "source" && f.sources == nil:
			f.sources = sourcesFromBlock(b.text)
		case b.kind == "author":
			sawAuthor = true
			label, names := authorBlock(b.text)
			if label != "" && f.authorLabel == "" {
				f.authorLabel = label
			}
			authors = append(authors, names...)
		case !seenQuestion && preTypes[b.kind]:
			pre = append(pre, rawLine(b))
		default:
			extra = append(extra, rawLine(b))
		}
	}
	if sawAuthor {
		if authors == nil {
			authors = []string{}
		}
		f.authors = authors
	}
	if len(pre) > 0 {
		f.preMarkup = strptr(strings.Join(pre, "\n"))
	}
	if len(extra) > 0 {
		f.extra = strptr(strings.Join(extra, "\n"))
	}
	return f
}

func sourcesFromBlock(text string) []string {
	t := strings.TrimSpace(text)
	if t == "" {
		return []string{""}
	}
	var items []string
	for _, line := range strings.Split(t, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		if line != "" {
			items = append(items, line)
		}
	}
	if len(items) == 0 {
		return []string{t}
	}
	return items
}

// authorLabels are the captions the card editor offers; the first is the one
// chgksuite prints, so only the others are written as an override.
func authorLabels() []string {
	a := xystrings.Default.Chgk.Author
	return []string{a.Default(), a.Feminine(), a.Plural(), a.FemininePlural()}
}

// authorBlock splits an "@" block into its caption override and its names.
func authorBlock(text string) (label string, names []string) {
	if l, rest, ok := override(text); ok {
		return l, splitNames(rest)
	}
	s := strings.TrimSpace(text)
	for _, lab := range authorLabels()[1:] {
		if strings.HasPrefix(s, "!!"+lab) {
			return lab, splitNames(s[2+len(lab):])
		}
	}
	return "", splitNames(s)
}

// override detects chgksuite's "!!Label " prefix on a field value.
func override(text string) (label, rest string, ok bool) {
	idx := strings.Index(text, " ")
	if idx == -1 || !strings.HasPrefix(text[:idx], "!!") {
		return "", text, false
	}
	return strings.ReplaceAll(text[2:idx], "~", " "), text[idx+1:], true
}

func splitNames(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func composeAuthors(names []string, label string) string {
	var clean []string
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			clean = append(clean, n)
		}
	}
	head := ""
	if label != "" {
		if enc := strings.Join(strings.Fields(label), "~"); enc != authorLabels()[0] {
			head = "!!" + enc
		}
	}
	body := strings.TrimSpace(head + " " + strings.Join(clean, ", "))
	if body == "" {
		return "@"
	}
	return "@ " + body
}

func composeSources(items []string) string {
	var clean []string
	for _, s := range items {
		if s = strings.TrimSpace(s); s != "" {
			clean = append(clean, s)
		}
	}
	switch len(clean) {
	case 0:
		return "^"
	case 1:
		return "^ " + clean[0]
	}
	return "^\n- " + strings.Join(clean, "\n- ")
}

// dropHandoutAnchor removes the first label-only handout bracket from question
// text: the field editor leaves one where a handout used to stand, and with no
// handout field to put back there it goes (chgk.ts insertInlineHandout with
// no handout).
func dropHandoutAnchor(q string) string {
	for _, sp := range inline.BracketSpans(q) {
		if !inline.IsHandoutBody(sp.Body) || handoutBracketContent(sp.Body) != "" {
			continue
		}
		return strings.TrimLeftFunc(strings.TrimRight(q[:sp.Start], " \t")+q[sp.End:], unicode.IsSpace)
	}
	return q
}

// handoutBracketContent is the text after a handout bracket's colon, "" for a
// bracket that holds the label alone.
func handoutBracketContent(body string) string {
	if _, after, ok := strings.Cut(body, ":"); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

// composeFields rebuilds a description in canonical field order.
func composeFields(f fields) string {
	var out []string
	marker := func(m, v string) {
		if v == "" {
			out = append(out, m)
		} else {
			out = append(out, m+" "+v)
		}
	}
	if f.preMarkup != nil && strings.TrimSpace(*f.preMarkup) != "" {
		out = append(out, strings.TrimSpace(*f.preMarkup))
	}
	if f.question != nil {
		marker("?", dropHandoutAnchor(*f.question))
	}
	if f.answer != nil {
		marker("!", *f.answer)
	}
	if f.zachet != nil {
		marker("=", *f.zachet)
	}
	if f.nezachet != nil {
		marker("!=", *f.nezachet)
	}
	if f.comment != nil {
		marker("/", *f.comment)
	}
	if f.sources != nil {
		out = append(out, composeSources(f.sources))
	}
	if f.authors != nil {
		out = append(out, composeAuthors(f.authors, f.authorLabel))
	}
	if f.extra != nil && strings.TrimSpace(*f.extra) != "" {
		out = append(out, strings.TrimSpace(*f.extra))
	}
	return strings.Join(out, "\n")
}

// splitVersions returns a Card's Version bodies, always at least one: text
// before the first separator is a Version too.
func splitVersions(desc string) []string {
	var bodies []string
	var cur []string
	seen := false
	flush := func() {
		body := strings.TrimSpace(strings.Join(cur, "\n"))
		if body != "" || seen {
			bodies = append(bodies, body)
		}
		cur = nil
	}
	for _, line := range strings.Split(desc, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if _, ok := VersionLineName(line); !ok {
			cur = append(cur, line)
			continue
		}
		if strings.TrimSpace(strings.Join(cur, "\n")) != "" || seen {
			flush()
		} else {
			cur = nil
		}
		seen = true
	}
	flush()
	if len(bodies) == 0 {
		bodies = []string{""}
	}
	return bodies
}

func versionNumber(i int) string { return strconv.Itoa(i + 1) }

func versionLabel(i int) string { return xystrings.Default.Import.Versions.Label(versionNumber(i)) }

// versionQuestion is one Version's question under its head. A Version that
// opens with a handout gets the head on a line of its own: on the handout's
// line it would make the handout part of a sentence, and the exports would
// print the bracket instead of the captioned box.
func versionQuestion(i int, q string) string {
	v := xystrings.Default.Import.Versions
	if pieces := inline.SplitHandouts(q); len(pieces) > 0 && pieces[0].Handout {
		return v.Head(versionNumber(i)) + "\n" + q
	}
	return v.Question(versionNumber(i), q)
}

// mergeField prints one value when every Version agrees and one labelled value
// per Version when they do not. A field a Version simply lacks counts as
// disagreement: inheriting it would put words in that Version's mouth.
func mergeField(values []*string) *string {
	same := true
	for _, v := range values[1:] {
		if (v == nil) != (values[0] == nil) || (v != nil && *v != *values[0]) {
			same = false
			break
		}
	}
	if same {
		return values[0]
	}
	var out []string
	for i, v := range values {
		if v != nil {
			out = append(out, versionLabel(i)+*v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return strptr(strings.Join(out, "\n"))
}

func sameList(a, b []string) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func allSame(values [][]string) bool {
	for _, v := range values[1:] {
		if !sameList(v, values[0]) {
			return false
		}
	}
	return true
}

func mergeSources(values [][]string) []string {
	if allSame(values) {
		return values[0]
	}
	var out []string
	for i, v := range values {
		if v != nil {
			var nonEmpty []string
			for _, s := range v {
				if s != "" {
					nonEmpty = append(nonEmpty, s)
				}
			}
			out = append(out, versionLabel(i)+strings.Join(nonEmpty, "; "))
		}
	}
	return out
}

// mergeAuthors folds into ONE "@" block: a second author marker reads as a
// different question's author.
func mergeAuthors(values [][]string) []string {
	if allSame(values) {
		return values[0]
	}
	var out []string
	for i, v := range values {
		if v != nil {
			out = append(out, versionLabel(i)+strings.Join(v, ", "))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return []string{strings.Join(out, "\n")}
}

// composeVersions folds a Card's Versions back into one question: the `?` field
// carries every wording page-broken, and any field the Versions disagree on
// prints each value labelled by its Version's number. Structural leftovers (a
// `№`, anything the fields do not model) come from Version 1.
func composeVersions(desc string) string {
	bodies := splitVersions(desc)
	if len(bodies) < 2 {
		return bodies[0]
	}
	fs := make([]fields, len(bodies))
	questions := make([]string, len(bodies))
	for i, b := range bodies {
		fs[i] = splitFields(b)
		questions[i] = versionQuestion(i, strings.TrimSpace(rawQuestion(b)))
	}
	pick := func(get func(fields) *string) []*string {
		out := make([]*string, len(fs))
		for i, f := range fs {
			out[i] = get(f)
		}
		return out
	}
	pickList := func(get func(fields) []string) [][]string {
		out := make([][]string, len(fs))
		for i, f := range fs {
			out[i] = get(f)
		}
		return out
	}
	return composeFields(fields{
		preMarkup:   fs[0].preMarkup,
		question:    strptr(strings.Join(questions, "\n"+pagebreak+"\n")),
		answer:      mergeField(pick(func(f fields) *string { return f.answer })),
		zachet:      mergeField(pick(func(f fields) *string { return f.zachet })),
		nezachet:    mergeField(pick(func(f fields) *string { return f.nezachet })),
		comment:     mergeField(pick(func(f fields) *string { return f.comment })),
		sources:     mergeSources(pickList(func(f fields) []string { return f.sources })),
		authors:     mergeAuthors(pickList(func(f fields) []string { return f.authors })),
		authorLabel: fs[0].authorLabel,
		extra:       fs[0].extra,
	})
}
