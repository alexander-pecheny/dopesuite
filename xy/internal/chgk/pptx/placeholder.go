package pptx

import (
	"fmt"
	"regexp"
	"strings"
)

// A slide added on a layout inherits that layout's placeholders — the title and
// the body ones, never the date, footer and slide-number ones, which is what
// python-pptx calls the cloneable set. The clone carries only the placeholder's
// identity; its geometry and its text styling stay inherited.

var (
	reLayoutShape = regexp.MustCompile(`(?s)<p:sp>.*?</p:sp>`)
	rePlaceholder = regexp.MustCompile(`<p:ph([^/>]*)/>`)
	reShapeName   = regexp.MustCompile(`<p:cNvPr id="\d+" name="([^"]*)"`)
	rePhType      = regexp.MustCompile(`type="([^"]*)"`)
	rePhIdx       = regexp.MustCompile(`idx="(\d+)"`)
	rePhOrient    = regexp.MustCompile(`orient="([^"]*)"`)
	rePhSz        = regexp.MustCompile(`sz="([^"]*)"`)
)

// placeholderBaseNames is _next_ph_name's table: a cloned placeholder is named
// after what it is, never after what the layout called it — a template written
// in Russian still yields "Title 1".
var placeholderBaseNames = map[string]string{
	"ctrTitle": "Title", "title": "Title", "subTitle": "Subtitle",
	"body": "Text Placeholder", "obj": "Content Placeholder", "": "Content Placeholder",
	"chart": "Chart Placeholder", "tbl": "Table Placeholder",
	"clipArt": "ClipArt Placeholder", "dgm": "SmartArt Placeholder",
	"media": "Media Placeholder", "pic": "Picture Placeholder",
	"dt": "Date Placeholder", "ftr": "Footer Placeholder",
	"hdr": "Header Placeholder", "sldNum": "Slide Number Placeholder",
}

// clonedPlaceholder is one placeholder copied off a layout.
type clonedPlaceholder struct {
	id             int
	name           string
	phType, phIdx  string
	phOrient, phSz string
	tf             *textFrame
	dropped        bool
}

func (p *clonedPlaceholder) render(s *slidePart) string {
	if p.dropped {
		return ""
	}
	var ph strings.Builder
	ph.WriteString("<p:ph")
	if p.phType != "" {
		fmt.Fprintf(&ph, ` type="%s"`, p.phType)
	}
	if p.phOrient != "" {
		fmt.Fprintf(&ph, ` orient="%s"`, p.phOrient)
	}
	if p.phSz != "" {
		fmt.Fprintf(&ph, ` sz="%s"`, p.phSz)
	}
	if p.phIdx != "" {
		fmt.Fprintf(&ph, ` idx="%s"`, p.phIdx)
	}
	ph.WriteString("/>")

	var b strings.Builder
	fmt.Fprintf(&b, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/>`, p.id, escapeAttr(p.name))
	b.WriteString(`<p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr>` + ph.String() + `</p:nvPr></p:nvSpPr><p:spPr/>`)
	b.WriteString(p.tf.render(s))
	b.WriteString(`</p:sp>`)
	return b.String()
}

// clonePlaceholders is python-pptx's clone_layout_placeholders.
func (p *pkg) clonePlaceholders(s *slidePart, layout string) []*clonedPlaceholder {
	xml := string(p.parts["ppt/"+strings.TrimPrefix(layout, "../")])
	var out []*clonedPlaceholder
	for _, sp := range reLayoutShape.FindAllString(xml, -1) {
		m := rePlaceholder.FindStringSubmatch(sp)
		if m == nil {
			continue
		}
		phType := ""
		if t := rePhType.FindStringSubmatch(m[1]); t != nil {
			phType = t[1]
		}
		if !cloneablePlaceholder(phType) {
			continue
		}
		attr := func(re *regexp.Regexp) string {
			if v := re.FindStringSubmatch(m[1]); v != nil {
				return v[1]
			}
			return ""
		}
		ph := &clonedPlaceholder{
			id: s.nextShapeID, phType: phType, phIdx: attr(rePhIdx),
			phOrient: attr(rePhOrient), phSz: attr(rePhSz), tf: newTextFrame(),
		}
		ph.name = nextPlaceholderName(s, phType, ph.phOrient, ph.id)
		s.nextShapeID++
		s.shapes = append(s.shapes, ph)
		out = append(out, ph)
	}
	return out
}

// nextPlaceholderName is _next_ph_name: the kind of placeholder, then a number
// one below the shape id, bumped until nothing on the slide has it.
func nextPlaceholderName(s *slidePart, phType, orient string, id int) string {
	base, known := placeholderBaseNames[phType]
	if !known {
		base = "Content Placeholder"
	}
	if orient == "vert" {
		base = "Vertical " + base
	}
	taken := map[string]bool{}
	for _, sh := range s.shapes {
		if ph, ok := sh.(*clonedPlaceholder); ok {
			taken[ph.name] = true
		}
	}
	for n := id - 1; ; n++ {
		name := fmt.Sprintf("%s %d", base, n)
		if !taken[name] {
			return name
		}
	}
}

// cloneablePlaceholder is iter_cloneable_placeholders: everything a slide is
// meant to fill in, and none of the running heads.
func cloneablePlaceholder(phType string) bool {
	switch phType {
	case "dt", "ftr", "sldNum":
		return false
	}
	return true
}
