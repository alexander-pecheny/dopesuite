package textparse

import (
	"code.pecheny.me/pecheny/dopesuite/chgksuite/typo"
	"strings"
)

// label splits a package into lines and types each by the marker it carries.
// Blank lines separate fragments; a fragment that holds an answer opens with
// its question even when the question carries no marker of its own.
func (p *parser) label(text string) []*elem {
	lineSep := "\n"
	if strings.Contains(text, "\r\n") {
		lineSep = "\r\n"
	}
	var out []*elem
	for _, frag := range strings.Split(text, lineSep+lineSep) {
		var fragment []*elem
		for _, line := range strings.Split(frag, lineSep) {
			if line != "" {
				fragment = append(fragment, &elem{"", typo.REW(line)})
			}
		}
		p.applyRegexes(&fragment)
		if len(fragment) > 0 && fragment[0].Type == "" && hasType(fragment, "answer") {
			fragment[0].Type = "question"
			fragment[0].Content = p.rx.field("number").ReplaceAllString(fragment[0].str(), "")
		}
		out = append(out, fragment...)
	}
	return out
}

var reUnderscore = strings.NewReplacer("_", "")

// applyRegexes ports ChgkParser.apply_regexes. When several field markers match
// one line, the line is split at their offsets and the pieces are spliced into
// the fragment as separate elements.
func (p *parser) applyRegexes(fragment *[]*elem) {
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
		for _, l := range p.rx.labelled {
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

func hasType(s []*elem, typ string) bool {
	for _, e := range s {
		if e.Type == typ {
			return true
		}
	}
	return false
}
