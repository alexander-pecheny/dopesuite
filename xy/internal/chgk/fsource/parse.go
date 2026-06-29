// Package fsource is a Go port of chgksuite's "4s" parser
// (chgksuite/composer/chgksuite_parser.py). It turns a 4s source string into the
// same [type, content] structure chgksuite's exporters consume, so xy can parse
// questions in-process instead of shelling out to Python. See parse_test.go for
// parity checks against chgksuite's own --debug structure dumps.
package fsource

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Pair is one [type, content] element of the parsed structure. Content is one of:
//   - string                      (scalar field / plain element text)
//   - []any                       (a list; see process_list — either a flat list
//     of string items, or the 2-element [preamble, []any{items…}] form)
//   - *Question                   (when Type == "Question")
//   - map[string]string           (the per-question "overrides" sub-object)
type Pair struct {
	Type    string
	Content any
}

// MarshalJSON emits a Pair as the JSON array [type, content] chgksuite uses.
func (p Pair) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{p.Type, p.Content})
}

// Doc is the whole parsed structure.
type Doc []Pair

// Question is an ordered map of a question's fields (insertion order preserved to
// match chgksuite's dict ordering). Values follow the same shapes as Pair.Content.
type Question struct {
	keys []string
	vals map[string]any
}

func newQuestion() *Question { return &Question{vals: map[string]any{}} }

func (q *Question) Has(k string) bool { _, ok := q.vals[k]; return ok }
func (q *Question) Get(k string) any  { return q.vals[k] }

func (q *Question) Set(k string, v any) {
	if _, ok := q.vals[k]; !ok {
		q.keys = append(q.keys, k)
	}
	q.vals[k] = v
}

func (q *Question) Keys() []string { return q.keys }
func (q *Question) empty() bool    { return len(q.keys) == 0 }

func (q *Question) keySet() map[string]bool {
	m := make(map[string]bool, len(q.keys))
	for _, k := range q.keys {
		m[k] = true
	}
	return m
}

// MarshalJSON emits the fields in insertion order.
func (q *Question) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range q.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(q.vals[k])
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// markerMapping mirrors the prefix→type table in parse_4s.
var markerMapping = map[string]string{
	"#":       "meta",
	"##":      "section",
	"###":     "heading",
	"###LJ":   "ljheading",
	"#B":      "battle",
	"#R":      "round",
	"#T":      "theme",
	"#EDITOR": "editor",
	"#DATE":   "date",
	"?":       "question",
	"№":       "number",
	"№№":      "setcounter",
	"!":       "answer",
	"=":       "zachet",
	"!=":      "nezachet",
	"^":       "source",
	"/":       "comment",
	"@":       "author",
	">":       "handout",
}

// questionLabels mirrors common.QUESTION_LABELS.
var questionLabels = map[string]bool{
	"handout": true, "question": true, "answer": true, "zachet": true,
	"nezachet": true, "comment": true, "source": true, "author": true,
	"number": true, "setcounter": true,
}

var requiredLabels = []string{"question", "answer"}

const overridePrefix = "!!"

// ── whitespace normalisation (typotools.remove_excessive_whitespace) ──
// WHITESPACE = {space, newline, nbsp(U+00A0)}.
var (
	reBadWspStart = regexp.MustCompile(`^[ \x{00a0}\n]+`)
	reBadWspEnd   = regexp.MustCompile(`[ \x{00a0}\n]+$`)
	reWsNl        = regexp.MustCompile(`\s+\n\s+`)
)

func rew(s string) string {
	s = reBadWspStart.ReplaceAllString(s, "")
	s = reBadWspEnd.ReplaceAllString(s, "")
	s = reWsNl.ReplaceAllString(s, "\n")
	return s
}

// firstField returns the first whitespace-delimited token (Python str.split()[0]).
func firstField(line string) (string, bool) {
	f := strings.Fields(line)
	if len(f) == 0 {
		return "", false
	}
	return f[0], true
}

// dropRunes returns line with its first n runes removed (Python line[len(marker):],
// which counts code points, not bytes).
func dropRunes(line string, n int) string {
	r := []rune(line)
	if n >= len(r) {
		return ""
	}
	return string(r[n:])
}

// ── counters (4SCOUNTER macros) ──
var reCounterUnify = regexp.MustCompile(`(4SCOUNTER([0-9a-zA-Z_]*)|set 4SCOUNTER([0-9a-zA-Z_]*) = ([0-9+]+))`)
var reSetCounter = regexp.MustCompile(`set 4SCOUNTER([0-9a-zA-Z_]*) = ([0-9+]+)`)

func replaceCounters(s string) string {
	dd := map[string]int{}
	get := func(id string) int {
		if _, ok := dd[id]; !ok {
			dd[id] = 1
		}
		return dd[id]
	}
	for {
		loc := reCounterUnify.FindStringSubmatchIndex(s)
		if loc == nil {
			break
		}
		matched := s[loc[0]:loc[1]]
		if reSetCounter.MatchString(matched) {
			m := reSetCounter.FindStringSubmatch(matched)
			id := m[1]
			// counter_value is [0-9+]+; Python int() of e.g. "3" → 3 (it never
			// actually contains '+', the regex just tolerates it).
			val, _ := strconv.Atoi(strings.TrimRight(m[2], "+"))
			dd[id] = val
			s = s[:loc[0]] + s[loc[1]:]
		} else {
			m := reCounterUnify.FindStringSubmatch(matched)
			id := m[2]
			v := get(id)
			s = s[:loc[0]] + strconv.Itoa(v) + s[loc[1]:]
			dd[id] = v + 1
		}
	}
	return s
}

// ── process_list: detect "- " list markers and reshape content ──
var reListDash = regexp.MustCompile(`(^|\n)- +`)

func processList(e *rawElem) {
	str, ok := e.Content.(string)
	if !ok || !strings.Contains(str, "-") {
		return
	}
	rawSp := strings.Split(str, "\n")
	sp := make([]string, len(rawSp))
	for i, x := range rawSp {
		sp[i] = rew(x)
	}
	var markers []int
	for i, x := range sp {
		if strings.HasPrefix(x, "-") {
			markers = append(markers, i)
		}
	}
	if len(markers) == 0 {
		return
	}
	preamble := strings.Join(sp[:markers[0]], "\n")
	var inner []any
	for num, index := range markers {
		var chunk string
		if num+1 == len(markers) {
			chunk = strings.Join(sp[index:], "\n")
		} else {
			chunk = strings.Join(sp[index:markers[num+1]], "\n")
		}
		// drop the leading "-" then trim
		inner = append(inner, rew(dropRunes(chunk, 1)))
	}
	switch {
	case len(inner) == 1:
		e.Content = rew(reListDash.ReplaceAllString(str, "$1"))
	case preamble != "":
		e.Content = []any{preamble, inner}
	default:
		e.Content = inner
	}
}

type rawElem struct {
	Type    string
	Content any
}

// Parse ports chgksuite's parse_4s. game mirrors the --game flag ("" = default;
// "si"/"troika"/"brain" tweak numbering). xy passes "" (chgk default).
func Parse(s string, game string) Doc {
	// strip a leading BOM
	if strings.HasPrefix(s, "\ufeff") && utf8.RuneCountInString(s) > 1 {
		s = s[len("\ufeff"):]
	}
	s = replaceCounters(s)

	// ── phase 1: raw line structure ──
	var structure []*rawElem
	for _, line := range strings.Split(s, "\n") {
		first, hasField := firstField(line)
		if rew(line) == "" || !hasField {
			structure = append(structure, &rawElem{"", ""})
			continue
		}
		if typ, ok := markerMapping[first]; ok {
			structure = append(structure, &rawElem{typ, rew(dropRunes(line, utf8.RuneCountInString(first)))})
		} else if len(structure) >= 1 {
			last := structure[len(structure)-1]
			if cur, ok := last.Content.(string); ok {
				last.Content = cur + "\n" + line
			}
		}
	}

	// ── phase 2: consolidate questions, number them ──
	var final Doc
	current := newQuestion()
	counter := 1

	emit := func() bool {
		// returns true if the question was emitted (or intentionally dropped)
		for _, lbl := range requiredLabels {
			if !current.Has(lbl) {
				return false // missing required → caller keeps current_question
			}
		}
		if current.Has("setcounter") {
			if n, err := strconv.Atoi(toStr(current.Get("setcounter"))); err == nil {
				counter = n
			}
		}
		if !current.Has("number") {
			current.Set("number", counter)
			counter++
		}
		final = append(final, Pair{"Question", current})
		current = newQuestion()
		return true
	}

	for _, e := range structure {
		processList(e)

		switch {
		case questionLabels[e.Type]:
			if current.empty() && e.Type != "question" && e.Type != "answer" && e.Type != "number" && e.Type != "setcounter" {
				final = append(final, Pair{e.Type, e.Content})
			} else if current.Has(e.Type) {
				current.Set(e.Type, mergeField(current.Get(e.Type), e.Content))
			} else {
				current.Set(e.Type, e.Content)
			}

		case e.Type == "":
			ks := current.keySet()
			onlySetcounter := len(ks) == 1 && ks["setcounter"]
			if !current.empty() && !onlySetcounter {
				emit() // on missing-required, emit() leaves current intact (mirrors `continue`)
			}

		default:
			switch {
			case e.Type == "theme":
				if game == "troika" {
					counter = 1
				} else {
					counter = 10
				}
			case (game == "brain" || game == "troika") && (e.Type == "battle" || e.Type == "section"):
				counter = 1
			}
			final = append(final, Pair{e.Type, e.Content})
		}
	}

	if !current.empty() {
		emit()
	}

	numberThemes(final, game)
	applyOverrides(final)
	return final
}

// mergeField ports the str/list merge cases when a field marker repeats.
func mergeField(cur, elem any) any {
	curStr, curIsStr := cur.(string)
	elemStr, elemIsStr := elem.(string)
	curList, curIsList := cur.([]any)
	elemList, elemIsList := elem.([]any)
	switch {
	case curIsStr && elemIsStr:
		return curStr + "\n" + elemStr
	case curIsList && elemIsStr:
		if len(curList) > 0 {
			curList[0] = toStr(curList[0]) + "\n" + elemStr
		}
		return curList
	case curIsStr && elemIsList:
		if len(elemList) >= 2 {
			return []any{toStr(elemList[0]) + "\n" + curStr, elemList[1]}
		}
		return elem
	case curIsList && elemIsList:
		if len(curList) >= 2 && len(elemList) >= 2 {
			curList[0] = toStr(curList[0]) + "\n" + toStr(elemList[0])
			if a, ok := curList[1].([]any); ok {
				if b, ok := elemList[1].([]any); ok {
					curList[1] = append(a, b...)
				}
			}
		}
		return curList
	}
	return cur
}

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// numberThemes ports the inline SI/troika theme numbering (final pass).
var reThemeLabel = regexp.MustCompile(`(?i)^тема\s+\d+(\s*\([^)]+\))?\.\s*(.+)`)

func numberThemes(final Doc, game string) {
	themeNumber := 0
	for i := range final {
		switch final[i].Type {
		case "battle", "section":
			themeNumber = 0
		case "theme":
			if raw, ok := final[i].Content.(string); ok {
				themeNumber++
				var name, label string
				if m := reThemeLabel.FindStringSubmatch(raw); m != nil {
					name = strings.TrimSpace(m[2])
					label = raw
				} else {
					name = raw
					label = "Тема " + strconv.Itoa(themeNumber) + ". " + name
				}
				q := newQuestion()
				q.Set("name", name)
				q.Set("number", themeNumber)
				q.Set("label", label)
				final[i].Content = q
			}
		}
	}
}

// applyOverrides ports the trailing "!!Label " field-label override pass.
func applyOverrides(final Doc) {
	fields := []string{"handout", "question", "answer", "zachet", "nezachet", "comment", "source", "author"}
	for _, p := range final {
		if p.Type != "Question" {
			continue
		}
		q, ok := p.Content.(*Question)
		if !ok {
			continue
		}
		for _, field := range fields {
			if !q.Has(field) {
				continue
			}
			val := q.Get(field)
			isList := false
			var scalar string
			if lst, ok := val.([]any); ok {
				isList = true
				if len(lst) > 0 {
					scalar = toStr(lst[0])
				}
			} else {
				scalar = toStr(val)
			}
			sp := strings.SplitN(scalar, " ", 2)
			if len(sp) == 1 {
				continue
			}
			sp1, sp2 := sp[0], sp[1]
			if strings.HasPrefix(sp1, overridePrefix) {
				ov, _ := q.Get("overrides").(map[string]string)
				if ov == nil {
					ov = map[string]string{}
					q.Set("overrides", ov)
				}
				ov[field] = strings.ReplaceAll(sp1[len(overridePrefix):], "~", " ")
				if isList {
					lst := q.Get(field).([]any)
					lst[0] = sp2
				} else {
					q.Set(field, sp2)
				}
			}
		}
	}
}
