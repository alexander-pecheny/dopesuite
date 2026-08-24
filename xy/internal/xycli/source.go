package xycli

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"xy/internal/chgk/fsource"
)

// The 4s a List exports as. The browser assembles it (web/ts/export.ts +
// versions.ts + the field layer of chgk.ts) and the server only renders what it
// is given, so a CLI export means this assembly exists twice. The corpus in
// testdata/exportsource.json is generated from the browser's own code
// (scripts/gen_source_fixture.js) and source_test.go holds this to it.

const pagebreak = "(PAGEBREAK)"

// markers, longest first — a "!=" must not be read as a "!". The table itself is
// fsource's, so Go and the browser cannot drift on what a marker is.
var markers = func() []struct{ marker, kind string } {
	types := fsource.MarkerTypes()
	out := make([]struct{ marker, kind string }, 0, len(types))
	for m, k := range types {
		out = append(out, struct{ marker, kind string }{m, k})
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].marker) != len(out[j].marker) {
			return len(out[i].marker) > len(out[j].marker)
		}
		return out[i].marker < out[j].marker
	})
	return out
}()

// typeMarker is the reverse table: the marker a block of each kind is written
// with, first spelling wins (as chgk.ts's TYPE_MARKER does).
var typeMarker = func() map[string]string {
	out := map[string]string{}
	for _, m := range markers {
		if _, seen := out[m.kind]; !seen {
			out[m.kind] = m.marker
		}
	}
	return out
}()

// preTypes are the blocks that, standing before the question, are pre-markup.
var preTypes = map[string]bool{
	"setcounter": true, "number": true, "meta": true, "section": true,
	"heading": true, "ljheading": true, "editor": true, "date": true,
}

var versionLine = regexp.MustCompile(`^\(hidden-comment\s+xy-version:([^()]*)\)$`)

// versionLineName reads a separator line's name: "" for the unnamed form, and
// ok=false when the line is no separator at all.
func versionLineName(line string) (string, bool) {
	m := versionLine.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

type block struct{ kind, text string }

func matchMarker(line string) (kind, rest string, ok bool) {
	for _, m := range markers {
		if line == m.marker {
			return m.kind, "", true
		}
		if strings.HasPrefix(line, m.marker+" ") {
			return m.kind, line[len(m.marker)+1:], true
		}
	}
	return "", "", false
}

// parseBlocks splits a description into its 4s elements: a marker opens one,
// every other line continues it, and text before any marker is a "pre" block.
// Version separators are xy's own metadata and drop out here.
func parseBlocks(desc string) []block {
	var blocks []block
	cur := -1
	for _, line := range strings.Split(desc, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if _, isVersion := versionLineName(line); isVersion {
			continue
		}
		if kind, rest, ok := matchMarker(line); ok {
			blocks = append(blocks, block{kind, rest})
			cur = len(blocks) - 1
			continue
		}
		if cur >= 0 {
			blocks[cur].text += "\n" + line
			continue
		}
		blocks = append(blocks, block{"pre", line})
		cur = len(blocks) - 1
	}
	out := blocks[:0]
	for _, b := range blocks {
		b.text = strings.TrimSpace(b.text)
		if b.text != "" || b.kind != "pre" {
			out = append(out, b)
		}
	}
	return out
}

func rawLine(b block) string {
	marker, ok := typeMarker[b.kind]
	if !ok {
		return b.text
	}
	if b.text == "" {
		return marker
	}
	return marker + " " + b.text
}

// fields is one question's structured form. A nil pointer is an absent field; a
// pointer to "" is a field present with no value (its bare marker).
type fields struct {
	preMarkup   *string
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

func splitFields(desc string) fields {
	var f fields
	var pre, extra, authors []string
	seenQuestion, sawAuthor := false, false
	for _, b := range parseBlocks(desc) {
		switch {
		case (b.kind == "question" || b.kind == "pre") && !seenQuestion:
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

var authorLabels = []string{"Автор", "Авторка", "Авторы", "Авторки"}

// authorBlock splits an "@" block into its caption override and its names.
func authorBlock(text string) (label string, names []string) {
	if l, rest, ok := override(text); ok {
		return l, splitNames(rest)
	}
	s := strings.TrimSpace(text)
	for _, lab := range authorLabels[1:] {
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
		if enc := strings.Join(strings.Fields(label), "~"); enc != authorLabels[0] {
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

// composeFields rebuilds a description in canonical field order. The handout
// rides inside the question text, so there is no separate handout field here —
// composeVersions is this package's only caller and keeps each version's own.
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
		marker("?", *f.question)
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

// SplitVersions returns a card's version bodies — always at least one, since
// text before the first separator is a version too.
func SplitVersions(desc string) []string {
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
		if _, ok := versionLineName(line); !ok {
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

// rawQuestion is the question block as written, handout bracket included.
func rawQuestion(desc string) string {
	for _, b := range parseBlocks(desc) {
		if b.kind == "question" || b.kind == "pre" {
			return b.text
		}
	}
	return ""
}

func versionLabel(i int) string { return "версия " + strconv.Itoa(i+1) + ": " }

// mergeField prints one value when every version agrees and one labelled value
// per version when they do not. A field a version simply lacks counts as
// disagreement — inheriting it would put words in that version's mouth.
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

func mergeSources(values [][]string) []string {
	same := true
	for _, v := range values[1:] {
		if !sameList(v, values[0]) {
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
	same := true
	for _, v := range values[1:] {
		if !sameList(v, values[0]) {
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
			out = append(out, versionLabel(i)+strings.Join(v, ", "))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return []string{strings.Join(out, "\n")}
}

// ComposeVersions folds a card's versions back into one question: the `?` field
// carries every wording page-broken, and any field the versions disagree on
// prints each value labelled by its version's number.
func ComposeVersions(desc string) string {
	bodies := SplitVersions(desc)
	if len(bodies) < 2 {
		return bodies[0]
	}
	fs := make([]fields, len(bodies))
	questions := make([]string, len(bodies))
	for i, b := range bodies {
		fs[i] = splitFields(b)
		questions[i] = "Версия " + strconv.Itoa(i+1) + ": " + strings.TrimSpace(rawQuestion(b))
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

// foldBlankLines turns a blank line inside a card into chgksuite's explicit
// (LINEBREAK): to 4s a blank line ends the element, so everything past it would
// fall out of the export.
func foldBlankLines(desc string) string {
	var out []string
	blanks := 0
	for _, line := range strings.Split(desc, "\n") {
		if strings.TrimSpace(line) == "" {
			blanks++
			continue
		}
		if blanks > 0 && len(out) > 0 {
			if _, _, isMarker := matchMarker(line); !isMarker {
				out[len(out)-1] += strings.Repeat("(LINEBREAK)", blanks)
			}
		}
		out = append(out, line)
		blanks = 0
	}
	return strings.Join(out, "\n")
}

// ExportSource is the 4s document a List exports as: its cards' descriptions in
// board order, versions folded back into one question each, blank-line separated.
func ExportSource(descs []string) string {
	var parts []string
	for _, desc := range descs {
		if s := foldBlankLines(strings.TrimSpace(ComposeVersions(desc))); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n") + "\n"
}
