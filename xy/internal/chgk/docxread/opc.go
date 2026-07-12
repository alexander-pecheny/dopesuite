package docxread

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strings"
)

// XML namespaces the converter cares about (parsing_engine.py's qn() prefixes).
const (
	nsW    = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	nsA    = "http://schemas.openxmlformats.org/drawingml/2006/main"
	nsR    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	nsV    = "urn:schemas-microsoft-com:vml"
	nsRels = "http://schemas.openxmlformats.org/package/2006/relationships"
	nsCT   = "http://schemas.openxmlformats.org/package/2006/content-types"
)

// node is a minimal stand-in for an lxml element: we need document order across
// mixed element types and arbitrary descendant lookups, neither of which
// encoding/xml's struct unmarshalling gives us.
type node struct {
	name  xml.Name
	attrs []xml.Attr
	// text mirrors lxml's .text — the character data before the first child
	// element, which is all _run_text ever reads (w:t has no element children).
	text string
	kids []*node
}

func (n *node) is(local string) bool { return n.name.Space == nsW && n.name.Local == local }

func (n *node) attr(space, local string) (string, bool) {
	if n == nil {
		return "", false
	}
	for _, a := range n.attrs {
		if a.Name.Local == local && (a.Name.Space == space || space == "") {
			return a.Value, true
		}
	}
	return "", false
}

// wattr is _attr(element, "w:name") — returns "" (and false) when absent.
func (n *node) wattr(local string) (string, bool) { return n.attr(nsW, local) }

// find is element.find(qn(...)): the first direct child with that name.
func (n *node) find(local string) *node {
	if n == nil {
		return nil
	}
	for _, k := range n.kids {
		if k.is(local) {
			return k
		}
	}
	return nil
}

// findAll is element.findall(qn(...)): direct children with that name.
func (n *node) findAll(local string) []*node {
	if n == nil {
		return nil
	}
	var out []*node
	for _, k := range n.kids {
		if k.is(local) {
			out = append(out, k)
		}
	}
	return out
}

// findPath is element.find("a/b"): a chain of first-child lookups.
func (n *node) findPath(locals ...string) *node {
	cur := n
	for _, l := range locals {
		cur = cur.find(l)
		if cur == nil {
			return nil
		}
	}
	return cur
}

// descendants is element.findall(".//tag") — descendants only, not self.
func (n *node) descendants(space, local string) []*node {
	var out []*node
	var walk func(*node)
	walk = func(p *node) {
		for _, k := range p.kids {
			if k.name.Space == space && k.name.Local == local {
				out = append(out, k)
			}
			walk(k)
		}
	}
	walk(n)
	return out
}

func parseXML(data []byte) (*node, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var root *node
	var stack []*node
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: t.Name, attrs: append([]xml.Attr(nil), t.Attr...)}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.kids = append(p.kids, n)
			} else if root == nil {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				cur := stack[len(stack)-1]
				if len(cur.kids) == 0 { // lxml .text stops at the first child
					cur.text += string(t)
				}
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("empty xml")
	}
	return root, nil
}

// ── the OPC package ─────────────────────────────────────────────────────────

type relationship struct {
	target   string // the raw Target attribute — python-docx's rel.target_ref
	external bool
}

type pkg struct {
	parts     map[string][]byte // part name ("/word/document.xml") → bytes
	overrides map[string]string // part name → content type
	defaults  map[string]string // lowercased extension → content type
	rels      map[string]relationship
}

// maxInflated bounds the total uncompressed size of a .docx's parts. A real
// tournament package with scanned handouts runs to tens of MB; anything past this
// is a zip bomb, not a question package.
const maxInflated = 256 << 20

func openPkg(data []byte) (*pkg, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	p := &pkg{
		parts:     map[string][]byte{},
		overrides: map[string]string{},
		defaults:  map[string]string{},
		rels:      map[string]relationship{},
	}
	// A .docx is a zip, so it can be a decompression bomb: the compressed upload
	// is bounded by the caller but the inflated parts are not. Cap the total we
	// will hold in memory.
	budget := int64(maxInflated)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, budget+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		if int64(len(b)) > budget {
			return nil, fmt.Errorf("docx is too large uncompressed (over %d bytes)", maxInflated)
		}
		budget -= int64(len(b))
		p.parts["/"+f.Name] = b
	}
	if err := p.readContentTypes(); err != nil {
		return nil, err
	}
	if err := p.readRels(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *pkg) readContentTypes() error {
	data, ok := p.parts["/[Content_Types].xml"]
	if !ok {
		return fmt.Errorf("not a docx: no [Content_Types].xml")
	}
	root, err := parseXML(data)
	if err != nil {
		return err
	}
	for _, k := range root.kids {
		if k.name.Space != nsCT {
			continue
		}
		switch k.name.Local {
		case "Default":
			ext, _ := k.attr("", "Extension")
			ct, _ := k.attr("", "ContentType")
			p.defaults[strings.ToLower(ext)] = ct
		case "Override":
			pn, _ := k.attr("", "PartName")
			ct, _ := k.attr("", "ContentType")
			p.overrides[pn] = ct
		}
	}
	return nil
}

// readRels loads word/_rels/document.xml.rels — the only rels the body needs
// (images and hyperlinks both hang off the document part).
func (p *pkg) readRels() error {
	data, ok := p.parts["/word/_rels/document.xml.rels"]
	if !ok {
		return nil
	}
	root, err := parseXML(data)
	if err != nil {
		return err
	}
	for _, k := range root.kids {
		if k.name.Space != nsRels || k.name.Local != "Relationship" {
			continue
		}
		id, _ := k.attr("", "Id")
		target, _ := k.attr("", "Target")
		mode, _ := k.attr("", "TargetMode")
		p.rels[id] = relationship{target: target, external: mode == "External"}
	}
	return nil
}

// partName resolves a relationship target (relative to word/) to a part name.
func partName(target string) string {
	if strings.HasPrefix(target, "/") {
		return path.Clean(target)
	}
	return path.Clean("/word/" + target)
}

func (p *pkg) contentType(name string) string {
	if ct, ok := p.overrides[name]; ok {
		return ct
	}
	if i := strings.LastIndex(name, "."); i >= 0 {
		if ct, ok := p.defaults[strings.ToLower(name[i+1:])]; ok {
			return ct
		}
	}
	return ""
}

func (p *pkg) xmlPart(name string) *node {
	data, ok := p.parts[name]
	if !ok {
		return nil
	}
	root, err := parseXML(data)
	if err != nil {
		return nil
	}
	return root
}
