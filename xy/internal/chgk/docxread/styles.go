package docxread

import (
	"strconv"
	"strings"
	"unicode"
)

// style is what python-docx's ParagraphStyle exposes to parsing_engine.py:
// the UI name, the styleId and the raw w:style element (for its w:pPr/w:numPr).
type style struct {
	name    string
	id      string
	element *node
}

// styles resolves a w:pPr/w:pStyle/@w:val the way python-docx's
// Styles.get_by_id does: unknown ids, ids of a non-paragraph style, and a
// missing pStyle all fall back to the default paragraph style.
type styles struct {
	byID    map[string]*style
	deflt   *style
	present bool
}

// babelFish is python-docx's BabelFish.internal2ui — the only style names whose
// styles.xml spelling differs from the UI one.
var babelFish = map[string]string{
	"caption":   "Caption",
	"footer":    "Footer",
	"header":    "Header",
	"heading 1": "Heading 1",
	"heading 2": "Heading 2",
	"heading 3": "Heading 3",
	"heading 4": "Heading 4",
	"heading 5": "Heading 5",
	"heading 6": "Heading 6",
	"heading 7": "Heading 7",
	"heading 8": "Heading 8",
	"heading 9": "Heading 9",
}

// onOff is ST_OnOff: everything but the false-ish spellings is on.
func onOff(v string, ok bool) bool {
	if !ok {
		return false
	}
	switch v {
	case "0", "false", "off":
		return false
	}
	return true
}

func loadStyles(p *pkg) *styles {
	s := &styles{byID: map[string]*style{}}
	root := p.xmlPart("/word/styles.xml")
	if root == nil {
		return s
	}
	s.present = true
	for _, el := range root.findAll("style") {
		typ, _ := el.wattr("type")
		if typ != "paragraph" {
			continue // get_by_id() ignores styles of the wrong type
		}
		id, _ := el.wattr("styleId")
		name := ""
		if n := el.find("name"); n != nil {
			name, _ = n.wattr("val")
			if ui, ok := babelFish[name]; ok {
				name = ui
			}
		}
		st := &style{name: name, id: id, element: el}
		s.byID[id] = st
		// The spec (and python-docx's default_for) takes the LAST default.
		if onOff(el.wattr("default")) {
			s.deflt = st
		}
	}
	return s
}

func (s *styles) get(id string) *style {
	if id != "" {
		if st, ok := s.byID[id]; ok {
			return st
		}
	}
	return s.deflt
}

// ── numbering (parsing_engine._Numbering) ───────────────────────────────────

type numLevel struct {
	fmt   string
	text  string
	start int
}

type numbering struct {
	numToAbstract map[string]string
	levels        map[[2]string]numLevel
	overrides     map[[2]string]int
}

func loadNumbering(p *pkg) *numbering {
	n := &numbering{
		numToAbstract: map[string]string{},
		levels:        map[[2]string]numLevel{},
		overrides:     map[[2]string]int{},
	}
	root := p.xmlPart("/word/numbering.xml")
	if root == nil {
		return n
	}
	for _, abstract := range root.findAll("abstractNum") {
		abstractID, ok := abstract.wattr("abstractNumId")
		if !ok {
			continue
		}
		for _, level := range abstract.findAll("lvl") {
			ilvl, ok := level.wattr("ilvl")
			if !ok {
				ilvl = "0"
			}
			lv := numLevel{fmt: "decimal", text: "%1.", start: 1}
			if f := level.find("numFmt"); f != nil {
				if v, ok := f.wattr("val"); ok {
					lv.fmt = v
				}
			}
			if t := level.find("lvlText"); t != nil {
				if v, ok := t.wattr("val"); ok {
					lv.text = v
				}
			}
			if s := level.find("start"); s != nil {
				lv.start = attrInt(s, "val", 1)
			}
			n.levels[[2]string{abstractID, ilvl}] = lv
		}
	}
	for _, num := range root.findAll("num") {
		numID, hasNum := num.wattr("numId")
		if abstract := num.find("abstractNumId"); abstract != nil {
			if abstractID, ok := abstract.wattr("val"); ok && hasNum {
				n.numToAbstract[numID] = abstractID
			}
		}
		for _, override := range num.findAll("lvlOverride") {
			ilvl, ok := override.wattr("ilvl")
			if !ok {
				ilvl = "0"
			}
			if s := override.find("startOverride"); s != nil {
				n.overrides[[2]string{numID, ilvl}] = attrInt(s, "val", 1)
			}
		}
	}
	return n
}

func attrInt(n *node, local string, deflt int) int {
	v, ok := n.wattr(local)
	if !ok {
		return deflt
	}
	i, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return deflt
	}
	return i
}

func (n *numbering) level(numID, ilvl string) (numLevel, bool) {
	abstractID, ok := n.numToAbstract[numID]
	if !ok {
		return numLevel{}, false
	}
	if lv, ok := n.levels[[2]string{abstractID, ilvl}]; ok {
		return lv, true
	}
	lv, ok := n.levels[[2]string{abstractID, "0"}]
	return lv, ok
}

// isOrdered: an unknown numId is assumed ordered (Python returns True when the
// level lookup fails), only an explicit bullet format is not.
func (n *numbering) isOrdered(numID, ilvl string) bool {
	lv, ok := n.level(numID, ilvl)
	if !ok {
		return true
	}
	return lv.fmt != "bullet"
}

// start is hardcoded to preserve_ol_start=False (chgk), so lists always restart
// at 1 — the overrides/start table is kept only because _Numbering builds it.
func (n *numbering) start(numID, ilvl string, preserveStart bool) int {
	if !preserveStart {
		return 1
	}
	if s, ok := n.overrides[[2]string{numID, ilvl}]; ok && s != 0 {
		return s
	}
	if lv, ok := n.level(numID, ilvl); ok {
		return lv.start
	}
	return 1
}

func (n *numbering) prefix(numID, ilvl string, value int) string {
	lv, ok := n.level(numID, ilvl)
	if !ok {
		lv = numLevel{fmt: "decimal", text: "%1."}
	}
	formatted := formatListNumber(value, lv.fmt)
	var prefix string
	if strings.Contains(lv.text, "%1") {
		prefix = strings.ReplaceAll(lv.text, "%1", formatted)
	} else {
		prefix = formatted + "."
	}
	return strings.TrimRightFunc(prefix, unicode.IsSpace) + " "
}

func formatListNumber(value int, format string) string {
	switch format {
	case "lowerLetter":
		return strings.ToLower(alphaNumber(value))
	case "upperLetter":
		return strings.ToUpper(alphaNumber(value))
	case "lowerRoman":
		return strings.ToLower(romanNumber(value))
	case "upperRoman":
		return strings.ToUpper(romanNumber(value))
	}
	return strconv.Itoa(value)
}

func alphaNumber(value int) string {
	result := ""
	for value > 0 {
		value--
		result = string(rune('A'+value%26)) + result
		value /= 26
	}
	if result == "" {
		return "A"
	}
	return result
}

func romanNumber(value int) string {
	numerals := []struct {
		n int
		s string
	}{
		{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"}, {100, "C"}, {90, "XC"},
		{50, "L"}, {40, "XL"}, {10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"},
	}
	var b strings.Builder
	for _, nm := range numerals {
		for value >= nm.n {
			b.WriteString(nm.s)
			value -= nm.n
		}
	}
	if b.Len() == 0 {
		return "I"
	}
	return b.String()
}
