package pptx

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"sort"
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
	// slideRelIDs is presentation.xml.rels' id per slide, in slide order; the
	// sldIdLst is written from it.
	slideRelIDs []string
}

// slidePart is one slide: its shapes, its relationships, and the layout it
// hangs off.
type slidePart struct {
	name   string // "ppt/slides/slide3.xml"
	layout string // "../slideLayouts/slideLayout12.xml"
	shapes []shape
	rels   []rel
	// raw is the template's own XML, kept for a slide the export edits rather
	// than builds (the title slide) and for a service slide it clones.
	raw []byte
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
	rels := p.relations("ppt/_rels/presentation.xml.rels")
	byID := map[string]string{}
	for _, r := range rels {
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
		p.slides = append(p.slides, &slidePart{
			name: name, layout: layout, raw: raw,
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
		name:        fmt.Sprintf("ppt/slides/slide%d.xml", len(p.slides)+1),
		layout:      layout,
		nextShapeID: 2,
	}
	s.rels = append(s.rels, rel{id: "rId1", relType: relLayout, target: layout})
	p.slides = append(p.slides, s)
	return s
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
		name = fmt.Sprintf("ppt/media/image%d.%s", len(p.media)+1, ext)
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
	for i, s := range p.slides {
		s.name = fmt.Sprintf("ppt/slides/slide%d.xml", i+1)
		xml, err := s.render()
		if err != nil {
			return nil, err
		}
		p.put(s.name, xml)
		p.put(relsPathOf(s.name), renderRels(s.rels))
	}
	// The relationships first: the sldIdLst is written from the ids they get.
	p.put("ppt/_rels/presentation.xml.rels", p.presentationRels())
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

// presentationRels keeps the template's own relationships and appends one per
// slide after the first, which is the order python-pptx adds them in.
func (p *pkg) presentationRels() []byte {
	rels := p.relations("ppt/_rels/presentation.xml.rels")
	kept := rels[:0:0]
	slideRels := 0
	for _, r := range rels {
		if r.relType != relSlide {
			kept = append(kept, r)
			continue
		}
		if slideRels < len(p.slides) {
			kept = append(kept, r)
		}
		slideRels++
	}
	next := len(rels) + 1
	for i := slideRels; i < len(p.slides); i++ {
		kept = append(kept, rel{
			id:      fmt.Sprintf("rId%d", next),
			relType: relSlide,
			target:  fmt.Sprintf("slides/slide%d.xml", i+1),
		})
		next++
	}
	p.slideRelIDs = nil
	for _, r := range kept {
		if r.relType == relSlide {
			p.slideRelIDs = append(p.slideRelIDs, r.id)
		}
	}
	return renderRels(kept)
}

func (p *pkg) presentationXML() []byte {
	var b strings.Builder
	b.WriteString("<p:sldIdLst>")
	// PowerPoint numbers slides from 256; the template's own is the first.
	for i := range p.slides {
		fmt.Fprintf(&b, `<p:sldId id="%d" r:id="%s"/>`, 256+i, p.slideRelIDs[i])
	}
	b.WriteString("</p:sldIdLst>")
	pres := string(p.parts["ppt/presentation.xml"])
	return []byte(reSldIDLst.ReplaceAllLiteralString(pres, b.String()))
}

func renderRels(rels []rel) []byte {
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
	for i := range p.slides {
		entries = append(entries, entry{"Override", fmt.Sprintf("/ppt/slides/slide%d.xml", i+1), ctSlide})
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

func escapeAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

func escapeText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
