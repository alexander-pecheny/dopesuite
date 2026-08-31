package pptx

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// pkg is the OOXML package a presentation is: the template's parts, read once,
// plus the slides and pictures the export adds. It is the pptx counterpart of
// what internal/chgk/docx does for a .docx, and it writes what python-pptx
// writes — the same XML declaration, the same skeleton, the same sorted
// [Content_Types].xml — so the two can be compared part for part.
type pkg struct {
	parts map[string][]byte
	order []string

	// slides are the presentation's slides in order: the template's own first,
	// then whatever was added.
	slides []*slidePart
	media  map[string]string // sha1 of the bytes → part name, so one picture is stored once
	// presRels is presentation.xml.rels as it stands: the template's own, minus
	// the slides taken out of it, plus one per slide added.
	presRels []rel
}

// slidePart is one slide: its shapes, its relationships, and the layout it
// hangs off.
type slidePart struct {
	name   string // "ppt/slides/slide3.xml"
	layout string // "../slideLayouts/slideLayout12.xml"
	// sldID and relID are what presentation.xml calls this slide. They are
	// assigned once and never reissued: python-pptx does not renumber a part
	// when another is removed, and a deck built on a template full of service
	// slides is full of the gaps that leaves.
	sldID  int
	relID  string
	shapes []shape
	rels   []rel
	// raw is the template's own XML, kept for a slide the export edits rather
	// than builds — the title slide.
	raw []byte
	// clonedBG and clonedShapes are a service slide's, lifted out of the
	// template slide it is a copy of.
	clonedBG, clonedShapes string
	// edits rewrite the placeholders of a template slide, one entry per shape.
	edits       []*editedShape
	nextShapeID int
}

type rel struct {
	id, relType, target string
	external            bool
}

const (
	relSlide     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide"
	relLayout    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout"
	relImage     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
	relHyperlink = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"

	ctSlide = "application/vnd.openxmlformats-officedocument.presentationml.slide+xml"

	// xmlDecl is lxml's, which is what python-pptx writes: single quotes.
	xmlDecl = "<?xml version='1.0' encoding='UTF-8' standalone='yes'?>\n"
)

// openPkg reads a template .pptx.
func openPkg(template []byte) (*pkg, error) {
	zr, err := zip.NewReader(bytes.NewReader(template), int64(len(template)))
	if err != nil {
		return nil, err
	}
	p := &pkg{parts: map[string][]byte{}, media: map[string]string{}}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		p.parts[f.Name] = data
		p.order = append(p.order, f.Name)
	}
	return p, p.readSlides()
}

var (
	reSldID     = regexp.MustCompile(`<p:sldId id="(\d+)" r:id="(rId\d+)"/>`)
	reRelEntry  = regexp.MustCompile(`<Relationship Id="(rId\d+)" Type="([^"]+)" Target="([^"]+)"(?: TargetMode="External")?/>`)
	reSldIDLst  = regexp.MustCompile(`(?s)<p:sldIdLst>.*?</p:sldIdLst>`)
	reLayoutRel = regexp.MustCompile(`Target="(\.\./slideLayouts/slideLayout\d+\.xml)"`)
)

// readSlides picks the template's own slides out of presentation.xml, in the
// order it lists them.
func (p *pkg) readSlides() error {
	pres := string(p.parts["ppt/presentation.xml"])
	p.presRels = p.relations("ppt/_rels/presentation.xml.rels")
	byID := map[string]string{}
	for _, r := range p.presRels {
		byID[r.id] = r.target
	}
	for _, m := range reSldID.FindAllStringSubmatch(pres, -1) {
		name := "ppt/" + byID[m[2]]
		raw, ok := p.parts[name]
		if !ok {
			return fmt.Errorf("template names a slide it does not carry: %s", name)
		}
		layout := ""
		if m := reLayoutRel.FindStringSubmatch(string(p.parts[relsPathOf(name)])); m != nil {
			layout = m[1]
		}
		id, _ := strconv.Atoi(m[1])
		p.slides = append(p.slides, &slidePart{
			name: name, layout: layout, raw: raw, sldID: id, relID: m[2],
			rels: p.relations(relsPathOf(name)), nextShapeID: 2,
		})
	}
	return nil
}

func (p *pkg) relations(path string) []rel {
	var out []rel
	for _, m := range reRelEntry.FindAllStringSubmatch(string(p.parts[path]), -1) {
		out = append(out, rel{id: m[1], relType: m[2], target: m[3],
			external: strings.Contains(m[0], `TargetMode="External"`)})
	}
	return out
}

func relsPathOf(part string) string {
	i := strings.LastIndex(part, "/")
	return part[:i] + "/_rels" + part[i:] + ".rels"
}

// addSlide appends an empty slide on the given layout, as python-pptx's
// add_slide does for a layout whose only placeholders are the footer ones the
// clone skips — which is every layout this template's config names.
func (p *pkg) addSlide(layout string) *slidePart {
	s := &slidePart{
		name:        p.nextPartName("ppt/slides/slide%d.xml"),
		layout:      layout,
		sldID:       p.nextSldID(),
		relID:       p.nextPresRelID(),
		nextShapeID: 2,
	}
	s.rels = append(s.rels, rel{id: "rId1", relType: relLayout, target: layout})
	p.presRels = append(p.presRels, rel{
		id: s.relID, relType: relSlide, target: strings.TrimPrefix(s.name, "ppt/"),
	})
	p.parts[s.name] = nil // claim the name so the next one does not take it
	p.order = append(p.order, s.name)
	p.slides = append(p.slides, s)
	return s
}

// nextPartName is python-pptx's next_partname: the lowest number nothing uses.
func (p *pkg) nextPartName(format string) string {
	for n := 1; ; n++ {
		name := fmt.Sprintf(format, n)
		if _, taken := p.parts[name]; !taken {
			return name
		}
	}
}

// nextImageName is next_image_partname: the number is the first free one across
// every image in the package, whatever extension it has, so a template holding
// image1.jpg and image2.png makes the next one image3.
func (p *pkg) nextImageName(ext string) string {
	used := map[int]bool{}
	for name := range p.parts {
		rest, ok := strings.CutPrefix(name, "ppt/media/image")
		if !ok {
			continue
		}
		if dot := strings.IndexByte(rest, '.'); dot >= 0 {
			if n, err := strconv.Atoi(rest[:dot]); err == nil {
				used[n] = true
			}
		}
	}
	for n := 1; ; n++ {
		if !used[n] {
			return fmt.Sprintf("ppt/media/image%d.%s", n, ext)
		}
	}
}

// nextSldID is CT_SlideIdList's: one past the highest, and never below 256.
func (p *pkg) nextSldID() int {
	next := 256
	for _, s := range p.slides {
		if s.sldID >= next {
			next = s.sldID + 1
		}
	}
	return next
}

func (p *pkg) nextPresRelID() string {
	for n := 1; ; n++ {
		id := fmt.Sprintf("rId%d", n)
		if !slices.ContainsFunc(p.presRels, func(r rel) bool { return r.id == id }) {
			return id
		}
	}
}

// removeSlide takes a slide out of the presentation, as _remove_slide_at does:
// the entry and the relationship go, and the part goes with them.
func (p *pkg) removeSlide(i int) {
	if i < 0 || i >= len(p.slides) {
		return
	}
	s := p.slides[i]
	p.slides = append(p.slides[:i], p.slides[i+1:]...)
	p.presRels = slices.DeleteFunc(p.presRels, func(r rel) bool { return r.id == s.relID })
	p.drop(s.name)
	p.drop(relsPathOf(s.name))
}

func (p *pkg) drop(name string) {
	delete(p.parts, name)
	p.order = slices.DeleteFunc(p.order, func(n string) bool { return n == name })
}

// layoutTarget is the relationship target for the nth layout of the master, in
// the order the master lists them — which is what `slide_layouts[n]` means.
func (p *pkg) layoutTarget(n int) (string, error) {
	master := string(p.parts["ppt/slideMasters/slideMaster1.xml"])
	rels := map[string]string{}
	for _, r := range p.relations("ppt/slideMasters/_rels/slideMaster1.xml.rels") {
		rels[r.id] = r.target
	}
	ids := regexp.MustCompile(`<p:sldLayoutId id="\d+" r:id="(rId\d+)"/>`).FindAllStringSubmatch(master, -1)
	if n < 0 || n >= len(ids) {
		return "", fmt.Errorf("the template has no layout %d", n)
	}
	return rels[ids[n][1]], nil
}

// addImage stores a picture once, keyed by its bytes, and relates it to the
// slide. It returns the relationship id the blip refers to.
func (s *slidePart) addImage(p *pkg, data []byte, ext string) string {
	sum := sha1.Sum(data)
	key := hex.EncodeToString(sum[:])
	name, seen := p.media[key]
	if !seen {
		name = p.nextImageName(ext)
		p.media[key] = name
		p.parts[name] = data
		p.order = append(p.order, name)
	}
	target := "../media/" + name[len("ppt/media/"):]
	for _, r := range s.rels {
		if r.relType == relImage && r.target == target {
			return r.id
		}
	}
	return s.addRel(relImage, target, false)
}

func (s *slidePart) addRel(relType, target string, external bool) string {
	id := fmt.Sprintf("rId%d", len(s.rels)+1)
	s.rels = append(s.rels, rel{id: id, relType: relType, target: target, external: external})
	return id
}

// save serializes the whole package: the template's parts as they were, the
// slides as they now are, and the three indexes that name them.
func (p *pkg) save() ([]byte, error) {
	for _, s := range p.slides {
		xml, err := s.render()
		if err != nil {
			return nil, err
		}
		p.put(s.name, xml)
		p.put(relsPathOf(s.name), renderRels(s.rels))
	}
	p.put("ppt/_rels/presentation.xml.rels", renderRels(p.presRels))
	p.put("ppt/presentation.xml", p.presentationXML())
	p.put("[Content_Types].xml", p.contentTypes())

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range p.order {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(p.parts[name]); err != nil {
			return nil, err
		}
	}
	// Close before reading the bytes: it writes the central directory.
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (p *pkg) put(name string, data []byte) {
	if _, seen := p.parts[name]; !seen {
		p.order = append(p.order, name)
	}
	p.parts[name] = data
}

// presentationXML rewrites the slide list from the slides that are left, each
// under the id it was given when it was added.
func (p *pkg) presentationXML() []byte {
	var b strings.Builder
	b.WriteString("<p:sldIdLst>")
	for _, s := range p.slides {
		fmt.Fprintf(&b, `<p:sldId id="%d" r:id="%s"/>`, s.sldID, s.relID)
	}
	b.WriteString("</p:sldIdLst>")
	pres := normalizeDecl(string(p.parts["ppt/presentation.xml"]))
	return []byte(reSldIDLst.ReplaceAllLiteralString(pres, b.String()))
}

// renderRels writes a .rels part. python-pptx sorts the entries by the number in
// their id, so a template whose own file is in some other order comes back
// sorted — and a slide's rels have to be written the same way to compare.
func renderRels(rels []rel) []byte {
	sorted := append([]rel(nil), rels...)
	slices.SortStableFunc(sorted, func(a, b rel) int { return relIDNum(a.id) - relIDNum(b.id) })
	rels = sorted

	var b strings.Builder
	b.WriteString(xmlDecl)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for _, r := range rels {
		fmt.Fprintf(&b, `<Relationship Id="%s" Type="%s" Target="%s"`, r.id, r.relType, escapeAttr(r.target))
		if r.external {
			b.WriteString(` TargetMode="External"`)
		}
		b.WriteString("/>")
	}
	b.WriteString("</Relationships>")
	return []byte(b.String())
}

var reCTEntry = regexp.MustCompile(`<(Default|Override) (Extension|PartName)="([^"]+)" ContentType="([^"]+)"/>`)

// contentTypes rewrites the index: a Default per picture extension, an Override
// per slide, both sorted the way python-pptx sorts them.
func (p *pkg) contentTypes() []byte {
	type entry struct{ kind, key, ct string }
	seen := map[string]bool{}
	var entries []entry
	for _, m := range reCTEntry.FindAllStringSubmatch(string(p.parts["[Content_Types].xml"]), -1) {
		if m[1] == "Override" && strings.HasPrefix(m[3], "/ppt/slides/") {
			continue
		}
		entries = append(entries, entry{m[1], m[3], m[4]})
		seen[m[1]+m[3]] = true
	}
	for _, name := range p.mediaNames() {
		ext := name[strings.LastIndex(name, ".")+1:]
		ct := imageContentTypes[ext]
		if ct == "" || seen["Default"+ext] {
			continue
		}
		entries = append(entries, entry{"Default", ext, ct})
		seen["Default"+ext] = true
	}
	for _, sl := range p.slides {
		entries = append(entries, entry{"Override", "/" + sl.name, ctSlide})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].kind != entries[j].kind {
			return entries[i].kind == "Default"
		}
		return entries[i].key < entries[j].key
	})

	var b strings.Builder
	b.WriteString(xmlDecl)
	b.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	for _, e := range entries {
		attr := "Extension"
		if e.kind == "Override" {
			attr = "PartName"
		}
		fmt.Fprintf(&b, `<%s %s="%s" ContentType="%s"/>`, e.kind, attr, escapeAttr(e.key), e.ct)
	}
	b.WriteString("</Types>")
	return []byte(b.String())
}

func (p *pkg) mediaNames() []string {
	var names []string
	for _, name := range p.media {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var imageContentTypes = map[string]string{
	"png": "image/png", "jpeg": "image/jpeg", "jpg": "image/jpeg",
	"gif": "image/gif", "bmp": "image/bmp", "tiff": "image/tiff", "webp": "image/webp",
}

// normalizeDecl swaps whatever wrote a part's XML declaration for lxml's, which
// is what python-pptx re-serializes every part it touches with.
func normalizeDecl(xml string) string {
	if !strings.HasPrefix(xml, "<?xml") {
		return xml
	}
	i := strings.Index(xml, "?>")
	if i < 0 {
		return xml
	}
	return xmlDecl + strings.TrimLeft(xml[i+2:], "\r\n")
}

func relIDNum(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "rId"))
	return n
}

func escapeAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

func escapeText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
