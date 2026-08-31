package pptx

import (
	"fmt"
	"regexp"
	"strings"
)

// The first title slide is the template's own, edited in place rather than
// built: chgksuite reuses slide one when the presentation still has only it, so
// whatever the template put on that slide — its background, its placeholder
// geometry, its creation ids — survives. Only the two placeholders' text bodies
// are rewritten, and the subtitle's whole shape is dropped when the package has
// no date.

var (
	reShapeSp  = regexp.MustCompile(`(?s)<p:sp>.*?</p:sp>`)
	reTxBody   = regexp.MustCompile(`(?s)<p:txBody>.*?</p:txBody>`)
	reShapeOff = regexp.MustCompile(`<a:off x="(-?\d+)" y="(-?\d+)"/><a:ext cx="(\d+)" cy="(\d+)"/>`)
	reSpTree   = regexp.MustCompile(`(?s)<p:spTree>.*</p:spTree>`)
	reSlideBG  = regexp.MustCompile(`(?s)<p:bg>.*?</p:bg>`)
	reRelAttr  = regexp.MustCompile(`(r:(?:id|embed|link)=")(rId\d+)(")`)
)

// remapRelIDs is _remap_relationship_ids: the copied shapes refer to the
// relationships of the slide they came from, which are not the ones they have
// now.
func remapRelIDs(xml string, m map[string]string) string {
	if len(m) == 0 {
		return xml
	}
	return reRelAttr.ReplaceAllStringFunc(xml, func(s string) string {
		parts := reRelAttr.FindStringSubmatch(s)
		if to, ok := m[parts[2]]; ok {
			return parts[1] + to + parts[3]
		}
		return s
	})
}

// editedShape is one placeholder of the template slide: the frame that replaces
// its text, or a request to remove the shape outright.
type editedShape struct {
	tf     *textFrame
	remove bool
	// xfrm overrides the placeholder's position, which is what a title slide
	// with no subtitle gets so the heading fills the slide.
	xfrm *[4]int64
}

// renderEdited rewrites the template slide's own placeholders.
func (s *slidePart) renderEdited() ([]byte, error) {
	xml := string(s.raw)
	// The template's own declaration is whatever wrote it; lxml rewrites it, and
	// so must this, or an edited slide differs from chgksuite's by its quotes.
	if i := strings.Index(xml, "?>"); i >= 0 && strings.HasPrefix(xml, "<?xml") {
		xml = xmlDecl + strings.TrimLeft(xml[i+2:], "\r\n")
	}
	shapes := reShapeSp.FindAllStringIndex(xml, -1)
	if len(s.edits) > len(shapes) {
		return nil, fmt.Errorf("the template's title slide has %d placeholders, not %d", len(shapes), len(s.edits))
	}
	// Back to front, so the offsets of the shapes still to do stay valid.
	for i := len(s.edits) - 1; i >= 0; i-- {
		e := s.edits[i]
		if e == nil {
			continue
		}
		start, end := shapes[i][0], shapes[i][1]
		if e.remove {
			xml = xml[:start] + xml[end:]
			continue
		}
		sp := xml[start:end]
		if e.tf != nil {
			body := e.tf.render(s)
			if loc := reTxBody.FindStringIndex(sp); loc != nil {
				sp = sp[:loc[0]] + body + sp[loc[1]:]
			}
		}
		if e.xfrm != nil {
			sp = setShapeXfrm(sp, *e.xfrm)
		}
		xml = xml[:start] + sp + xml[end:]
	}
	return []byte(xml), nil
}

// spTreeShapes lifts a slide's own shapes out of its XML: everything in the
// shape tree after the group's properties, which is what _clone_slide copies.
func spTreeShapes(xml string) string {
	tree := reSpTree.FindString(xml)
	if tree == "" {
		return ""
	}
	end := strings.Index(tree, "</p:grpSpPr>")
	if end >= 0 {
		tree = tree[end+len("</p:grpSpPr>"):]
	} else if i := strings.Index(tree, "<p:grpSpPr/>"); i >= 0 {
		tree = tree[i+len("<p:grpSpPr/>"):]
	}
	tree = strings.TrimSuffix(tree, "</p:spTree>")
	if i := strings.Index(tree, "<p:extLst>"); i >= 0 {
		tree = tree[:i]
	}
	return tree
}

// slideBackground is cSld's own <p:bg>, which a clone takes with it.
func slideBackground(xml string) string { return reSlideBG.FindString(xml) }

// setShapeXfrm gives a placeholder an explicit position, which it inherits from
// the layout until something moves it.
func setShapeXfrm(sp string, box [4]int64) string {
	xfrm := fmt.Sprintf(`<a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>`, box[0], box[1], box[2], box[3])
	if strings.Contains(sp, "<a:off ") {
		return reShapeOff.ReplaceAllString(sp,
			fmt.Sprintf(`<a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/>`, box[0], box[1], box[2], box[3]))
	}
	if strings.Contains(sp, "<p:spPr/>") {
		return strings.Replace(sp, "<p:spPr/>", "<p:spPr>"+xfrm+"</p:spPr>", 1)
	}
	return strings.Replace(sp, "<p:spPr>", "<p:spPr>"+xfrm, 1)
}
