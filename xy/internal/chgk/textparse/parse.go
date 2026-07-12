// Package textparse is a Go port of chgksuite's ChgkParser (chgksuite/parser.py):
// it turns the plain text of a question package into the same [type, content]
// structure the 4s exporters consume, so xy can import a .docx or .txt package
// in-process instead of shelling out to Python.
//
// Only the "chgk" game is ported (the SI/troika parsers in the same Python file
// are a separate line-based state machine and xy has no use for them).
//
// The port is deliberately literal, quirks included — see parse_test.go, which
// checks byte-parity against chgksuite's own canonical outputs. Where the Python
// does something that looks like a bug, a comment says so rather than "fixing" it.
package textparse

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/typo"
)

const sep = "\n"

// elem is one [type, content] pair of the working structure. content is a string
// for all but a handful of elements (a source can become a list of references).
type elem struct {
	Type    string
	Content any
}

func (e *elem) str() string {
	if s, ok := e.Content.(string); ok {
		return s
	}
	return ""
}

// questionLabels mirrors common.QUESTION_LABELS — the fields that belong to a
// question rather than standing on their own in the structure.
var questionLabels = map[string]bool{
	"handout": true, "question": true, "answer": true, "zachet": true,
	"nezachet": true, "comment": true, "source": true, "author": true,
	"number": true, "setcounter": true,
}

// zeroPrefixes are the two ways a package can mark an unnumbered warm-up question.
var zeroPrefixes = []string{"Нулевой вопрос", "Разминочный вопрос"}

// handoutLabel is labels_ru.toml's question_labels.handout.
const handoutLabel = "Раздаточный материал"

type parser struct {
	structure []*elem
	opts      typo.Options
	// now is "today" for the date sanity check; injectable so tests are stable.
	now time.Time
}

// Options controls the few knobs xy actually varies.
type Options struct {
	// SingleNumberLines mirrors args.single_number_line_handling
	// ("smart" | "on" | "off"). Empty means "smart", chgksuite's default.
	SingleNumberLines string
}

// Parse ports ChgkParser.parse. text is the plain text of the package (from a
// .txt file or from docxread.ToText).
func Parse(text string, opts Options) fsource.Doc {
	p := &parser{opts: typo.DefaultOptions(), now: time.Now()}
	return p.parse(text, opts)
}

func (p *parser) parse(text string, opts Options) fsource.Doc {
	// ── 1. split into blank-line-separated fragments and label what we can ──
	lineSep := "\n"
	if strings.Contains(text, "\r\n") {
		lineSep = "\r\n"
	}
	for _, frag := range strings.Split(text, lineSep+lineSep) {
		var fragment []*elem
		for _, line := range strings.Split(frag, lineSep) {
			if line == "" { // Python: `for xx in x.split(sep) if xx`
				continue
			}
			fragment = append(fragment, &elem{"", typo.REW(line)})
		}
		applyRegexes(&fragment)
		// A fragment containing an answer must start with the question, even if
		// the question carried no marker of its own.
		hasAnswer := false
		for _, e := range fragment {
			if e.Type == "answer" {
				hasAnswer = true
			}
		}
		if hasAnswer && len(fragment) > 0 && fragment[0].Type == "" {
			fragment[0].Type = "question"
		}
		p.structure = append(p.structure, fragment...)
	}

	p.doEnumerateHack()
	for _, e := range p.structure {
		if e.Type == "handout" {
			e.Type = "question"
		}
	}
	p.processSingleNumberLines(opts.SingleNumberLines)
	p.dupletBlitzHack()

	// ── 2. glue unlabelled lines onto the field they continue ──
	p.mergeYToX("question", "answer")
	p.mergeToXUntilNextField("answer")
	p.mergeToXUntilNextField("comment")

	// ── 3. make sure every answer has a question in front of it ──
	p.ensureQuestions()
	for _, e := range p.structure {
		if e.Type == "newquestion" {
			e.Type = "question"
		}
	}
	p.dirtyMergeToXUntilNextField("source")

	// A bare "Автор:" line means the author's name is on the next line.
	for i := 0; i < len(p.structure); i++ {
		if p.structure[i].Type == "author" &&
			reAuthorOnly.MatchString(typo.REW(p.structure[i].str())) &&
			i+1 < len(p.structure) {
			p.mergeToPrevious(i + 1)
		}
	}

	p.mergeToXUntilNextField("zachet")
	p.mergeToXUntilNextField("nezachet")

	// ── 4. drop empties, strip the field labels off the content ──
	kept := p.structure[:0]
	for _, e := range p.structure {
		if e.Type == "" && typo.REW(e.str()) == "" {
			continue
		}
		kept = append(kept, e)
	}
	p.structure = kept
	if len(p.structure) == 0 {
		return nil
	}
	if p.structure[0].Type == "" && reNumber.MatchString(typo.REW(p.structure[0].str())) {
		p.mergeToNext(0)
	}
	p.stripLabels()

	// ── 5. prettify: pull out numbers, detect inner lists, typography ──
	p.prettify()

	// ── 6. pack fields into questions ──
	final := p.pack()

	// ── 7. header/date detection and per-question postprocessing ──
	final = p.headerPass(final)
	for _, pr := range final {
		if pr.Type == "Question" {
			p.postprocessQuestion(pr.Content.(*fsource.Question))
		}
	}
	return final
}

// ── structure surgery (the merge_* helpers) ─────────────────────────────────

// at emulates Python's negative indexing: structure[i-1] with i == 0 is the LAST
// element, not an error. Several loops below rely on that, deliberately or not.
func (p *parser) at(i int) *elem {
	if i < 0 {
		i += len(p.structure)
	}
	return p.structure[i]
}

func (p *parser) pop(i int) *elem {
	e := p.structure[i]
	p.structure = append(p.structure[:i], p.structure[i+1:]...)
	return e
}

func (p *parser) insert(i int, e *elem) {
	p.structure = append(p.structure, nil)
	copy(p.structure[i+1:], p.structure[i:])
	p.structure[i] = e
}

func (p *parser) mergeToPrevious(i int) {
	target := p.at(i - 1)
	popped := p.pop(i)
	if target.str() != "" {
		target.Content = target.str() + sep + popped.str()
	} else {
		target.Content = popped.Content
	}
}

func (p *parser) mergeToNext(i int) {
	popped := p.pop(i)
	p.structure[i].Content = popped.str() + sep + p.structure[i].str()
}

func (p *parser) findNextFieldName(i int) string {
	t := i + 1
	if t >= len(p.structure) {
		return ""
	}
	for t < len(p.structure)-1 && p.structure[t].Type == "" {
		t++
	}
	return p.structure[t].Type
}

// hardBoundaries are the structural markers a question/answer merge must not
// swallow (chgksuite's ChgkParser.HARD_BOUNDARIES).
var hardBoundaries = map[string]bool{
	"battle": true, "section": true, "tour": true, "tourrev": true,
	"heading": true, "editor": true, "date": true,
}

func (p *parser) mergeYToX(x, y string) {
	for i := 0; i < len(p.structure); i++ {
		if p.structure[i].Type != x {
			continue
		}
		for i+1 < len(p.structure) &&
			p.structure[i+1].Type != y &&
			!hardBoundaries[p.structure[i+1].Type] {
			p.mergeToPrevious(i + 1)
		}
	}
}

// badNextFields: an unlabelled line must not be glued onto the previous field if
// what follows it is a question or an answer — it belongs to that instead.
var badNextFields = map[string]bool{"question": true, "answer": true}

func (p *parser) mergeToXUntilNextField(x string) {
	for i := 0; i < len(p.structure); i++ {
		if p.structure[i].Type != x {
			continue
		}
		for i+1 < len(p.structure) &&
			p.structure[i+1].Type == "" &&
			!badNextFields[p.findNextFieldName(i)] {
			p.mergeToPrevious(i + 1)
		}
	}
}

func (p *parser) dirtyMergeToXUntilNextField(x string) {
	for i := 0; i < len(p.structure); i++ {
		if p.structure[i].Type != x {
			continue
		}
		for i+1 < len(p.structure) && p.structure[i+1].Type == "" {
			p.mergeToPrevious(i + 1)
		}
	}
}

// ── step 1 helpers ──────────────────────────────────────────────────────────

var reUnderscore = strings.NewReplacer("_", "")

// applyRegexes ports ChgkParser.apply_regexes. When several field markers match
// one line, the line is split at their offsets and the pieces are spliced into
// the fragment as separate elements.
func applyRegexes(fragment *[]*elem) {
	st := *fragment
	for i := 0; i < len(st); i++ {
		// Offsets are measured on the underscore-stripped text but used to slice
		// the original — a chgksuite bug that only bites lines mixing italics with
		// two field markers, faithfully reproduced.
		probe := reUnderscore.Replace(st[i].str())
		type hit struct {
			name  string
			start int
		}
		var hits []hit
		for _, l := range labelled {
			if loc := l.re.FindStringIndex(probe); loc != nil {
				hits = append(hits, hit{l.name, loc[0]})
			}
		}
		if len(hits) == 0 {
			continue
		}
		if len(hits) == 1 {
			st[i].Type = hits[0].name
			continue
		}
		// sorted(matching_regexes, key=lambda x: x[1]) — Python's set iteration
		// order makes ties arbitrary; sort by name as a tiebreak for determinism.
		for a := 1; a < len(hits); a++ {
			for b := a; b > 0 && (hits[b].start < hits[b-1].start ||
				(hits[b].start == hits[b-1].start && hits[b].name < hits[b-1].name)); b-- {
				hits[b], hits[b-1] = hits[b-1], hits[b]
			}
		}
		full := st[i].str()
		var slices []*elem
		for j := 1; j < len(hits); j++ {
			end := len(full)
			if j+1 < len(hits) {
				end = hits[j+1].start
			}
			slices = append(slices, &elem{hits[j].name, sliceStr(full, hits[j].start, end)})
		}
		// chgksuite does `for slice_ in slices: st.insert(i + 1, slice_)`, so each
		// insert pushes the previous one right and the tail ends up REVERSED.
		// Reproduced verbatim — it only shows with 3+ markers on one line.
		for _, sl := range slices {
			st = append(st, nil)
			copy(st[i+2:], st[i+1:])
			st[i+1] = sl
		}
		st[i].Type = hits[0].name
		st[i].Content = sliceStr(full, 0, hits[1].start)
	}
	*fragment = st
}

// sliceStr clamps a byte-offset slice; offsets come from the underscore-stripped
// probe string and can therefore run past the end of the original.
func sliceStr(s string, start, end int) string {
	if start > len(s) {
		start = len(s)
	}
	if end > len(s) {
		end = len(s)
	}
	if end < start {
		end = start
	}
	return s[start:end]
}

var reNumStart = regexp.MustCompile(`^([0-9]+)\.`)

// doEnumerateHack ports do_enumerate_hack: an unlabelled "N. …" line right after
// an author is only a question if an answer shows up nearby — otherwise it's a
// numbered item of some regulation block.
func (p *parser) doEnumerateHack() {
	const lookahead = 12
	blockEnd := map[string]bool{
		"editor": true, "tour": true, "tourrev": true,
		"date": true, "heading": true, "battle": true,
	}
	prevType := ""
	for i, e := range p.structure {
		if e.Type == "" && prevType == "author" && reNumStart.MatchString(e.str()) {
			hasAnswerSoon := false
			for j := i + 1; j < i+1+lookahead && j < len(p.structure); j++ {
				nt := p.structure[j].Type
				if nt == "answer" || nt == "handout" || nt == "question" {
					hasAnswerSoon = true
					break
				}
				if blockEnd[nt] {
					break
				}
			}
			if hasAnswerSoon {
				e.Type = "question"
				e.Content = strings.TrimSpace(reNumStart.ReplaceAllString(e.str(), ""))
			}
		}
		if e.Type != "" {
			prevType = e.Type
		}
	}
}

var reNumOnly = regexp.MustCompile(`^([0-9]+)\.?$`)

// processSingleNumberLines ports process_single_number_lines: some packages put
// the question number on a line of its own. In "smart" mode that's only assumed
// when no question is marked at all and the numbers are spaced plausibly.
func (p *parser) processSingleNumberLines(mode string) {
	if mode == "" {
		mode = "smart"
	}
	if mode == "off" {
		return
	}
	type numLine struct {
		idx, num int
	}
	var lines []numLine
	for i, e := range p.structure {
		if e.Type != "" {
			continue
		}
		m := reNumOnly.FindStringSubmatch(strings.TrimSpace(e.str()))
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		lines = append(lines, numLine{i, n})
	}
	patch := func(l numLine) {
		p.structure[l.idx].Type = "question"
		// question_stub for ru: "Вопрос {}."
		p.structure[l.idx].Content = "Вопрос " + strconv.Itoa(l.num) + "."
	}
	if mode == "on" {
		for _, l := range lines {
			patch(l)
		}
		return
	}
	// smart
	for _, e := range p.structure {
		if e.Type == "question" {
			return
		}
	}
	if len(lines) == 0 {
		return
	}
	frac := float64(len(p.structure)) / float64(len(lines))
	if frac < 4.0 || frac > 13.0 {
		return
	}
	var prev *numLine
	for i := range lines {
		l := lines[i]
		if prev == nil || (l.num-prev.num <= 3 && l.idx-prev.idx > 1) {
			patch(l)
			prev = &lines[i]
		}
	}
}

// dupletBlitzHack ports the "Дуплет."/"Блиц." special case (chgksuite issue #23).
// Note the Python operator precedence: a lone "Дуплет." forces a question
// unconditionally, while "Блиц." is also gated on the surrounding elements.
func (p *parser) dupletBlitzHack() {
	for i, e := range p.structure {
		words := strings.Fields(e.str())
		hasDuplet, hasBlitz := false, false
		for _, w := range words {
			if w == "Дуплет." {
				hasDuplet = true
			}
			if w == "Блиц." {
				hasBlitz = true
			}
		}
		if hasDuplet || (hasBlitz && e.Type != "question" &&
			(i == 0 || p.structure[i-1].Type != "question")) {
			e.Type = "question"
		}
	}
}

// ── step 3 ──────────────────────────────────────────────────────────────────

func (p *parser) ensureQuestions() {
	for i := 0; i < len(p.structure); i++ {
		if p.structure[i].Type != "answer" {
			continue
		}
		prev := p.at(i - 1).Type // i == 0 wraps to the last element, as in Python
		if prev == "question" || prev == "newquestion" {
			continue
		}
		p.insert(i, &elem{"newquestion", ""})
		i = -1 // Python sets i = 0 and then increments
	}

	for i := 0; i < len(p.structure)-1; i++ {
		if p.structure[i].Type != "" || p.structure[i+1].Type != "newquestion" {
			continue
		}
		p.mergeToNext(i)
		cur := typo.REW(p.structure[i].str())
		prev := typo.REW(p.at(i - 1).str())
		if reNumber.MatchString(cur) && !reNumber.MatchString(prev) {
			p.structure[i].Type = "question"
			p.structure[i].Content = reNumber.ReplaceAllString(cur, "")
			// Python does int(num.group(0)) on a match like "12. ", which always
			// raises ValueError and is swallowed — so no number is ever inserted.
			// Reproduced by only inserting when the match parses as an integer.
			if m := reNumber.FindString(typo.REW(p.structure[i].str())); m != "" {
				if n, err := strconv.Atoi(m); err == nil {
					p.insert(i, &elem{"number", n})
				}
			}
		}
		i = -1
	}
}

// ── step 4: strip the label text off each labelled element ──────────────────

func (p *parser) stripLabels() {
	for idx := 0; idx < len(p.structure); idx++ {
		e := p.structure[idx]
		if e.Type == "" {
			e.Type = "meta"
		}
		re, known := byName[e.Type]
		if !known || e.Type == "tour" || e.Type == "tourrev" || e.Type == "editor" || e.Type == "battle" {
			continue
		}
		beforeReplacement := ""
		if e.Type == "question" {
			num, hasNum := questionNumber(e.str())
			if hasNum {
				p.insert(idx, &elem{"number", num})
				idx++
			}
			if !hasNum {
				low := strings.ToLower(e.str())
				if strings.Contains(low, "нулевой вопрос") || strings.Contains(low, "разминочный вопрос") {
					p.insert(idx, &elem{"number", "0"})
					idx++
				}
			}
			lines := strings.Split(e.str(), sep)
			var out []string
			for _, line := range lines {
				if reQuestion.MatchString(line) {
					line = replaceFirst(reQuestion, line, "")
				}
				if s := strings.TrimSpace(line); s != "" {
					out = append(out, s)
				}
			}
			e.Content = strings.Join(out, sep)
		} else {
			beforeReplacement = e.str()
			e.Content = replaceFirst(re, e.str(), "")
		}
		if strings.HasPrefix(e.str(), sep) {
			e.Content = strings.TrimPrefix(e.str(), sep)
		}
		// The gendered "Авторка:" label is preserved as a 4s field-label override.
		if e.Type == "author" && beforeReplacement != "" &&
			strings.Contains(strings.ToLower(beforeReplacement), "авторка:") {
			e.Content = "!!Авторка" + e.str()
		}
	}
}

// questionNumber returns the (?P<number>…) group of the question regex, and
// whether it matched and was non-empty.
func questionNumber(s string) (string, bool) {
	m := reQuestion.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	n := m[reQuestion.SubexpIndex("number")]
	return n, n != ""
}

func replaceFirst(re *regexp.Regexp, s, with string) string {
	loc := re.FindStringIndex(s)
	if loc == nil {
		return s
	}
	return s[:loc[0]] + with + s[loc[1]:]
}

// ── step 5: numbers, inner lists, typography ────────────────────────────────

func (p *parser) prettify() {
	for id := 0; id < len(p.structure); id++ {
		e := p.structure[id]

		if e.Type == "question" {
			if m := reQuestion.FindStringSubmatch(e.str()); m != nil {
				p.insert(id, &elem{"number", m[reQuestion.SubexpIndex("number")]})
				id++
			}
			e.Content = reQuestion.ReplaceAllString(e.str(), "")
		}

		p.detectInnerList(e)

		if e.Type == "source" {
			if s, ok := e.Content.(string); ok && len(splitLines(s)) > 1 {
				var items []any
				for _, x := range splitLines(s) {
					items = append(items, replaceFirst(reNumber, typo.REW(x), ""))
				}
				e.Content = items
			}
		}

		if e.Type != "date" {
			e.Content = recursiveTypography(e.Content, p.opts)
		}
	}
}

var reCRLF = regexp.MustCompile(`\r?\n`)

func splitLines(s string) []string { return reCRLF.Split(s, -1) }

// reListItem is the inner-list marker "N. " / "N) ". The original also has a
// trailing `\s*(?!\d)`, which RE2 can't express; listItemEnd below does that part.
var reListItem = regexp.MustCompile(`([\s\x{00a0}]+|^)(\d+)[\.\)]`)

// listItemEnd resolves the `\s*(?!\d)` tail. Because \s* is greedy and backtracks,
// the lookahead only actually rejects a marker when NO whitespace follows it and
// the next character is a digit ("1.2" is not a list item, "1. 2" is — with the
// match then ending before the digit). It returns the end offset, or -1 to reject.
func listItemEnd(s string, end int) int {
	maxWS := end
	for maxWS < len(s) && isPySpace(rune(s[maxWS])) {
		maxWS++
	}
	for e := maxWS; e >= end; e-- {
		if e >= len(s) || s[e] < '0' || s[e] > '9' {
			return e
		}
	}
	return -1
}

// detectInnerList ports the "detect inner lists" block: a run of 1., 2., 3. …
// markers turns the element's content into a [preamble, items] list. For a
// question this only applies to Дуплет/Блиц (whose parts really are a list);
// elsewhere (sources, comments) any ascending run counts.
func (p *parser) detectInnerList(e *elem) {
	s, ok := e.Content.(string)
	if !ok {
		return
	}
	type match struct {
		text     string
		num      int
		start    int
		endAfter int
	}
	var all []match
	for _, loc := range reListItem.FindAllStringSubmatchIndex(s, -1) {
		numStr := s[loc[4]:loc[5]]
		n, err := strconv.Atoi(numStr)
		if err != nil {
			continue
		}
		wsEnd := listItemEnd(s, loc[1])
		if wsEnd < 0 {
			continue
		}
		all = append(all, match{s[loc[0]:wsEnd], n, loc[0], wsEnd})
	}
	if len(all) <= 1 {
		return
	}
	sorted := append([]match(nil), all...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].num < sorted[j-1].num; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	var candidate []match
	for j := 0; j < len(sorted) && j == sorted[j].num-1; j++ {
		candidate = append(candidate, sorted[j])
	}
	if len(candidate) <= 1 {
		return
	}
	for i := 1; i < len(candidate); i++ {
		if candidate[i].start <= candidate[i-1].start {
			return // false positive: a duplicate number matched at the wrong place
		}
	}
	isQuestion := e.Type == "question"
	low := strings.ToLower(s)
	// Python: element[0] != "question" or (element[0]=="question" and "дуплет" in …) or "блиц" in …
	if !(!isQuestion || (isQuestion && strings.Contains(low, "дуплет")) || strings.Contains(low, "блиц")) {
		return
	}
	positions := make([]int, len(candidate))
	for i, c := range candidate {
		positions[i] = c.start
	}
	parts := partition(s, positions)
	for i, c := range candidate {
		parts[i+1] = strings.Replace(parts[i+1], c.text, "", 1)
	}
	items := make([]any, 0, len(parts)-1)
	for _, x := range parts[1:] {
		items = append(items, x)
	}
	if parts[0] != "" {
		e.Content = []any{parts[0], items}
	} else {
		e.Content = items
	}
}

// isPySpace matches Python's \s (Unicode-aware, so NBSP counts).
func isPySpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f', 0x00a0:
		return true
	}
	return false
}

func partition(s string, indices []int) []string {
	out := make([]string, 0, len(indices)+1)
	prev := 0
	for _, i := range indices {
		out = append(out, s[prev:i])
		prev = i
	}
	return append(out, s[prev:])
}

func recursiveTypography(v any, o typo.Options) any {
	switch t := v.(type) {
	case string:
		return typo.Typography(t, o)
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = recursiveTypography(x, o)
		}
		return out
	}
	return v
}

// ── step 6: pack fields into questions ──────────────────────────────────────

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
		final = append(final, fsource.Pair{Type: "Question", Content: cur})
	}
	return final
}

// ── step 7: header, date, per-question cleanup ──────────────────────────────

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
			today := p.now.Truncate(24 * time.Hour)
			if d.Year() >= 1980 && (d.Before(today) || d.Sub(today) <= 365*24*time.Hour) {
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
		final = append(fsource.Doc{{Type: "ljheading", Content: final[0].Content}}, final...)
		// Python keeps using the stale `fq` after this insert, so the date scan
		// below covers one element fewer than it looks like it does. Kept as is.
	}
	for i := 0; !dateDefined && i < fq && i < len(final); i++ {
		content, ok := final[i].Content.(string)
		if !ok {
			continue
		}
		n := float64(utf8.RuneCountInString(content)) / 10
		if m := reDate2.FindString(content); m != "" && float64(utf8.RuneCountInString(m)) >= n {
			final[i].Type = "date"
			dateDefined = true
			break
		}
		if m := p.searchForDate(content); m != "" && float64(utf8.RuneCountInString(m)) >= n {
			final[i].Type = "date"
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
	// "Раздаточный материал:\n[…" → "[Раздаточный материал:\n…" so the handout
	// ends up inside the bracketed block the composer looks for.
	if m := reHandoutBracket.FindString(qs); m != "" {
		gap := reHandoutBracket.FindStringSubmatch(qs)[1]
		q.Set("question", replaceDeep(q.Get("question"), m, "["+handoutLabel+":"+gap))
	}
}

var reHandoutBracket = regexp.MustCompile(`(?s)` + regexp.QuoteMeta(handoutLabel) + `:([ \n]+)\[`)

// fieldRegexes is the regexes[k] lookup _try_extract_field uses.
var fieldRegexes = map[string]*regexp.Regexp{
	"zachet": reZachet, "nezachet": reNezachet, "source": reSource,
	"comment": reComment, "author": reAuthor,
}

// tryExtractField ports _try_extract_field: a field that never got its own line
// (e.g. a "Зачёт:" buried at the end of the answer) is pulled out of whichever
// field currently holds it.
func (p *parser) tryExtractField(q *fsource.Question, k string) {
	re := fieldRegexes[k]
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
