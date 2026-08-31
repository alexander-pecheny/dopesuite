package pptx

import (
	"fmt"
	"strings"
)

// A slide's geometry is in EMU, English Metric Units, which is what OOXML
// measures in: 914400 to the inch and 12700 to the point.
const (
	emuPerInch = 914400
	emuPerPt   = 12700
	pxPerInch  = 96
	ptPerInch  = 72
)

func inches(v float64) int64 { return int64(v * emuPerInch) }
func points(v float64) int64 { return int64(v * emuPerPt) }

type shape interface {
	render(s *slidePart) string
}

// textbox is a plain text box: python-pptx's add_textbox, with the word wrap and
// the "no autofit" chgksuite always sets on it.
type textbox struct {
	id                     int
	left, top, width, high int64
	tf                     *textFrame
}

// textFrame holds a shape's paragraphs. It also carries the insets, which the
// fit measurement subtracts, and the vertical anchor.
type textFrame struct {
	paragraphs []*paragraph
	// marginLeft…marginBottom are the text insets. OOXML's defaults are 0.1"
	// left/right and 0.05" top/bottom; python-pptx reports those when the XML
	// says nothing, and the measurement subtracts them.
	marginLeft, marginRight int64
	marginTop, marginBottom int64
	verticalAnchor          string
	// placeholder is set on the template title slide's own shapes, which are
	// edited rather than built, so they keep their XML and only their text
	// changes.
	placeholder bool
}

func newTextFrame() *textFrame {
	return &textFrame{
		marginLeft: inches(0.1), marginRight: inches(0.1),
		marginTop: inches(0.05), marginBottom: inches(0.05),
	}
}

// paragraph is one <a:p>. size is the paragraph's own font size in points,
// which its runs inherit unless they say otherwise.
type paragraph struct {
	runs        []*run
	size        float64
	fontName    string
	align       string
	lineSpacing float64 // a multiplier
	lineSpacePt float64 // an exact height in points; wins over the multiplier
	spaceAfter  float64 // points
	spaceBefore float64 // points
}

// run is one <a:r>, or — when br is set — one <a:br/>, which is how a line
// break inside a paragraph is spelled.
type run struct {
	text                  string
	br                    bool
	size                  float64
	fontName              string
	bold, italic, strike  bool
	underline             bool
	color                 string // "RRGGBB"
	hyperlink             string // the URL, already quoted
	hyperlinkRelID        string
	language              string
	sizeSet, boldSet      bool
	italicSet, strikeSet  bool
	underlineSet, langSet bool
}

func (t *textbox) render(s *slidePart) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="TextBox %d"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>`, t.id, t.id-1)
	fmt.Fprintf(&b, `<p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>`, t.left, t.top, t.width, t.high)
	b.WriteString(`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/></p:spPr>`)
	b.WriteString(t.tf.render(s))
	b.WriteString(`</p:sp>`)
	return b.String()
}

// picture is python-pptx's add_picture: the bytes go in as a part, the shape
// refers to them by relationship.
type picture struct {
	id                     int
	name                   string
	relID                  string
	left, top, width, high int64
}

func (p *picture) render(*slidePart) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<p:pic><p:nvPicPr><p:cNvPr id="%d" name="Picture %d" descr="%s"/>`, p.id, p.id-1, escapeAttr(p.name))
	b.WriteString(`<p:cNvPicPr><a:picLocks noChangeAspect="1"/></p:cNvPicPr><p:nvPr/></p:nvPicPr>`)
	fmt.Fprintf(&b, `<p:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></p:blipFill>`, p.relID)
	fmt.Fprintf(&b, `<p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>`, p.left, p.top, p.width, p.high)
	b.WriteString(`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr></p:pic>`)
	return b.String()
}

func (f *textFrame) render(s *slidePart) string {
	var b strings.Builder
	b.WriteString(`<p:txBody><a:bodyPr wrap="square"`)
	var attrs []string
	if f.marginLeft != inches(0.1) {
		attrs = append(attrs, fmt.Sprintf(`lIns="%d"`, f.marginLeft))
	}
	if f.marginTop != inches(0.05) {
		attrs = append(attrs, fmt.Sprintf(`tIns="%d"`, f.marginTop))
	}
	if f.marginRight != inches(0.1) {
		attrs = append(attrs, fmt.Sprintf(`rIns="%d"`, f.marginRight))
	}
	if f.marginBottom != inches(0.05) {
		attrs = append(attrs, fmt.Sprintf(`bIns="%d"`, f.marginBottom))
	}
	if f.verticalAnchor != "" {
		attrs = append(attrs, fmt.Sprintf(`anchor="%s"`, f.verticalAnchor))
	}
	for _, a := range attrs {
		b.WriteString(" " + a)
	}
	b.WriteString(`><a:noAutofit/></a:bodyPr><a:lstStyle/>`)
	for _, p := range f.paragraphs {
		b.WriteString(p.render(s))
	}
	b.WriteString(`</p:txBody>`)
	return b.String()
}

func (p *paragraph) render(s *slidePart) string {
	var b strings.Builder
	b.WriteString("<a:p>")
	if pr := p.propsXML(); pr != "" {
		b.WriteString(pr)
	}
	for _, r := range p.runs {
		if r.br {
			b.WriteString("<a:br/>")
			continue
		}
		b.WriteString(r.render(s))
	}
	b.WriteString("</a:p>")
	return b.String()
}

// propsXML is <a:pPr>. Its attributes come first and its children in schema
// order: lnSpc, spcBef, spcAft, then defRPr.
func (p *paragraph) propsXML() string {
	var attrs, children strings.Builder
	if p.align != "" {
		fmt.Fprintf(&attrs, ` algn="%s"`, p.align)
	}
	switch {
	case p.lineSpacePt > 0:
		fmt.Fprintf(&children, `<a:lnSpc><a:spcPts val="%d"/></a:lnSpc>`, int(p.lineSpacePt*100))
	case p.lineSpacing > 0:
		fmt.Fprintf(&children, `<a:lnSpc><a:spcPct val="%d"/></a:lnSpc>`, int(p.lineSpacing*100000))
	}
	if p.spaceBefore > 0 {
		fmt.Fprintf(&children, `<a:spcBef><a:spcPts val="%d"/></a:spcBef>`, int(p.spaceBefore*100))
	}
	if p.spaceAfter > 0 {
		fmt.Fprintf(&children, `<a:spcAft><a:spcPts val="%d"/></a:spcAft>`, int(p.spaceAfter*100))
	}
	if def := defRPrXML(p.size, p.fontName); def != "" {
		children.WriteString(def)
	}
	if attrs.Len() == 0 && children.Len() == 0 {
		return ""
	}
	if children.Len() == 0 {
		return "<a:pPr" + attrs.String() + "/>"
	}
	return "<a:pPr" + attrs.String() + ">" + children.String() + "</a:pPr>"
}

func defRPrXML(size float64, fontName string) string {
	if size <= 0 && fontName == "" {
		return ""
	}
	var attrs, children strings.Builder
	if size > 0 {
		fmt.Fprintf(&attrs, ` sz="%d"`, int(size*100))
	}
	if fontName != "" {
		fmt.Fprintf(&children, `<a:latin typeface="%s"/>`, escapeAttr(fontName))
	}
	if children.Len() == 0 {
		return "<a:defRPr" + attrs.String() + "/>"
	}
	return "<a:defRPr" + attrs.String() + ">" + children.String() + "</a:defRPr>"
}

// render writes <a:r>. The attribute order is python-pptx's — sz, lang, then the
// toggles — and the children are in the schema's: solidFill, latin, hlinkClick.
func (r *run) render(*slidePart) string {
	var attrs, children strings.Builder
	if r.sizeSet && r.size > 0 {
		fmt.Fprintf(&attrs, ` sz="%d"`, int(r.size*100))
	}
	if r.langSet && r.language != "" {
		fmt.Fprintf(&attrs, ` lang="%s"`, r.language)
	}
	if r.boldSet && r.bold {
		attrs.WriteString(` b="1"`)
	}
	if r.italicSet && r.italic {
		attrs.WriteString(` i="1"`)
	}
	if r.underlineSet && r.underline {
		attrs.WriteString(` u="sng"`)
	}
	if r.strikeSet && r.strike {
		attrs.WriteString(` strike="sngStrike"`)
	}
	if r.color != "" {
		fmt.Fprintf(&children, `<a:solidFill><a:srgbClr val="%s"/></a:solidFill>`, r.color)
	}
	if r.fontName != "" {
		fmt.Fprintf(&children, `<a:latin typeface="%s"/>`, escapeAttr(r.fontName))
	}
	if r.hyperlinkRelID != "" {
		fmt.Fprintf(&children, `<a:hlinkClick r:id="%s"/>`, r.hyperlinkRelID)
	}

	var b strings.Builder
	b.WriteString("<a:r>")
	switch {
	case attrs.Len() == 0 && children.Len() == 0:
	case children.Len() == 0:
		b.WriteString("<a:rPr" + attrs.String() + "/>")
	default:
		b.WriteString("<a:rPr" + attrs.String() + ">" + children.String() + "</a:rPr>")
	}
	b.WriteString("<a:t>" + escapeText(r.text) + "</a:t></a:r>")
	return b.String()
}

// slideSkeleton is python-pptx's CT_Slide.new(): the empty slide every added one
// starts as. Its namespace order is a, p, r — the template's own slides declare
// them a, r, p, which is why an edited slide keeps its bytes instead.
const slideSkeleton = xmlDecl +
	`<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"` +
	` xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"` +
	` xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
	`<p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
	`<p:grpSpPr/>%s</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`

func (s *slidePart) render() ([]byte, error) {
	if s.raw != nil {
		return s.renderEdited()
	}
	var b strings.Builder
	b.WriteString(s.clonedShapes)
	for _, sh := range s.shapes {
		b.WriteString(sh.render(s))
	}
	out := fmt.Sprintf(slideSkeleton, b.String())
	if s.clonedBG != "" {
		out = strings.Replace(out, "<p:cSld>", "<p:cSld>"+s.clonedBG, 1)
	}
	return []byte(out), nil
}

// addTextbox is python-pptx's shapes.add_textbox.
func (s *slidePart) addTextbox(left, top, width, high int64) *textbox {
	t := &textbox{id: s.nextShapeID, left: left, top: top, width: width, high: high, tf: newTextFrame()}
	s.nextShapeID++
	s.shapes = append(s.shapes, t)
	return t
}

// addPicture is python-pptx's shapes.add_picture.
func (s *slidePart) addPicture(p *pkg, name string, data []byte, left, top, width, high int64) *picture {
	relID := s.addImage(p, data, imageExt(name))
	pic := &picture{id: s.nextShapeID, name: name, relID: relID,
		left: left, top: top, width: width, high: high}
	s.nextShapeID++
	s.shapes = append(s.shapes, pic)
	return pic
}

func imageExt(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		ext := strings.ToLower(name[i+1:])
		if ext == "jpg" {
			return "jpeg"
		}
		return ext
	}
	return "png"
}

// addParagraph is text_frame.add_paragraph.
func (f *textFrame) addParagraph() *paragraph {
	p := &paragraph{}
	f.paragraphs = append(f.paragraphs, p)
	return p
}

// first is text_frame.paragraphs[0]: a frame always has one.
func (f *textFrame) first() *paragraph {
	if len(f.paragraphs) == 0 {
		return f.addParagraph()
	}
	return f.paragraphs[0]
}
