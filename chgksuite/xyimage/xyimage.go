// Package xyimage reads and writes xy's Edited Image, the .xyimage file
// (xy ADR-0024): a picture made in xy's image editor, kept as its Recipe, its
// untouched pictures, its vector overlays and its raster rendition.
//
// The container is framed like PNG, so a reader can always tell what it does
// not understand:
//
//	"XYIMG" 0x00 | u8 format version
//	chunks: [4-byte ASCII type][u32 big-endian length][bytes]
//	  RCPE  the Recipe, UTF-8 JSON, always first
//	  IMAG  one picture as it was added, numbered by its order (repeatable)
//	  OVER  one vector overlay: black boxes and frozen lines (repeatable)
//	  RAST  the raster rendition, a PNG of the whole frame
//
// A chunk type that starts with an uppercase letter is critical: a reader that
// does not know it refuses the file rather than dropping something silently.
// A lowercase one is optional and skipped. The Recipe carries its own version
// and grows by optional fields; the format byte changes only if the framing
// does.
//
// The browser writes these (web/ts/xyimagefile.ts, which reads the same
// fixtures); the exporters read them, taking the vector rendition for typst
// and the raster one for everything else.
package xyimage

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Magic opens every .xyimage; Version is the framing this package writes.
const (
	Magic   = "XYIMG\x00"
	Version = 1
)

// MIME is the type xy stores an Edited Image under, and Ext the extension its
// files carry. Readers still tell one by its bytes (Is); these are for the
// places that only have a name to go on, like an import picking pictures out
// of an archive.
const (
	MIME = "image/x-xyimage"
	Ext  = ".xyimage"
)

// The chunk types this version knows.
const (
	TypeRecipe  = "RCPE"
	TypeImage   = "IMAG"
	TypeOverlay = "OVER"
	TypeRaster  = "RAST"
)

const (
	typeLen   = 4
	lengthLen = 4
	headerLen = len(Magic) + 1
	// pxPerInch is chgksuite's reading of a picture's pixels (inline.Img):
	// an Edited Image reports its frame at this density as its native size.
	pxPerInch = 120
	mmPerInch = 25.4
	// coordDecimals is how finely the composed SVG states a length in mm.
	coordDecimals = 3
	floatBits     = 64
)

var (
	// ErrNotXyimage is bytes that do not start with Magic.
	ErrNotXyimage = errors.New("xyimage: not an Edited Image")
	// ErrNewer is a file made by a newer xy: an unknown format version or an
	// unknown critical chunk.
	ErrNewer = errors.New("xyimage: made by a newer xy")
	// ErrCorrupt is a file whose framing or Recipe does not hold together.
	ErrCorrupt = errors.New("xyimage: damaged file")
)

// Layer is one step of the bake plan, in stacking order, in millimetres from
// the frame's top-left. A picture layer names an IMAG chunk by index and
// carries its rectangle; an overlay layer names an OVER chunk and covers the
// whole frame.
type Layer struct {
	Kind string  `json:"kind"` // "image" or "svg"
	Src  int     `json:"src,omitempty"`
	Over int     `json:"over,omitempty"`
	Ext  string  `json:"ext,omitempty"` // "png" or "jpg", for an image
	X    float64 `json:"x,omitempty"`
	Y    float64 `json:"y,omitempty"`
	W    float64 `json:"w,omitempty"`
	H    float64 `json:"h,omitempty"`
}

// Face is one uploaded font file an image's text is set in (xy ADR-0025): a
// file inside a Font Family's zip, which is named by its SHA-256. The overlays
// name it by Family, a name of the editor's own making, so neither typst nor
// the browser ever confuses it with another font that calls itself the same.
// Name is the Family as the user knows it, for messages.
type Face struct {
	Zip    string `json:"zip"`
	Path   string `json:"face"`
	Family string `json:"family"`
	Name   string `json:"name,omitempty"`
}

// Bake is the part of the Recipe an exporter reads: the frame, its layers and
// the uploaded Faces its overlays use. Text in the bundled Noto Sans needs no
// Face.
type Bake struct {
	W      float64 `json:"w"`
	H      float64 `json:"h"`
	BG     string  `json:"bg"`
	Layers []Layer `json:"layers"`
	Fonts  []Face  `json:"fonts,omitempty"`
}

// recipeHead is what this package reads out of the Recipe; the editor's own
// fields (the items, their text and styling) pass through untouched.
type recipeHead struct {
	V    int  `json:"v"`
	Bake Bake `json:"bake"`
}

// File is one parsed .xyimage. Recipe is kept as its bytes, so writing a file
// back changes nothing the editor wrote.
type File struct {
	Recipe   []byte
	Images   [][]byte
	Overlays [][]byte
	Raster   []byte
	Bake     Bake
}

// Is reports whether b starts like an Edited Image. Exporters ask by the
// bytes, not the name: a renamed file is still one, and a file merely named
// .xyimage is not.
func Is(b []byte) bool { return bytes.HasPrefix(b, []byte(Magic)) }

func critical(t string) bool { return t[0] >= 'A' && t[0] <= 'Z' }

// Parse reads an Edited Image.
func Parse(b []byte) (*File, error) {
	if !Is(b) {
		return nil, ErrNotXyimage
	}
	if len(b) < headerLen {
		return nil, ErrCorrupt
	}
	if b[len(Magic)] != Version {
		return nil, ErrNewer
	}
	f := &File{}
	for rest := b[headerLen:]; len(rest) > 0; {
		if len(rest) < typeLen+lengthLen {
			return nil, ErrCorrupt
		}
		t := string(rest[:typeLen])
		n := binary.BigEndian.Uint32(rest[typeLen : typeLen+lengthLen])
		rest = rest[typeLen+lengthLen:]
		if uint64(n) > uint64(len(rest)) {
			return nil, ErrCorrupt
		}
		if err := f.take(t, rest[:n]); err != nil {
			return nil, err
		}
		rest = rest[n:]
	}
	return f, f.readRecipe()
}

func (f *File) take(t string, data []byte) error {
	if f.Recipe == nil && t != TypeRecipe {
		return ErrCorrupt // the Recipe comes first
	}
	switch t {
	case TypeRecipe:
		f.Recipe = data
	case TypeImage:
		f.Images = append(f.Images, data)
	case TypeOverlay:
		f.Overlays = append(f.Overlays, data)
	case TypeRaster:
		f.Raster = data
	default:
		if critical(t) {
			return ErrNewer
		}
	}
	return nil
}

func (f *File) readRecipe() error {
	var head recipeHead
	if err := json.Unmarshal(f.Recipe, &head); err != nil {
		return ErrCorrupt
	}
	f.Bake = head.Bake
	for _, l := range f.Bake.Layers {
		if !f.layerOK(l) {
			return ErrCorrupt
		}
	}
	return nil
}

func (f *File) layerOK(l Layer) bool {
	switch l.Kind {
	case "image":
		return l.Src >= 0 && l.Src < len(f.Images) && (l.Ext == "png" || l.Ext == "jpg")
	case "svg":
		return l.Over >= 0 && l.Over < len(f.Overlays)
	}
	return false
}

// Encode writes the file back: the Recipe, then every picture, every overlay
// and the raster, in that order.
func (f *File) Encode() []byte {
	var b bytes.Buffer
	b.WriteString(Magic)
	b.WriteByte(Version)
	chunk(&b, TypeRecipe, f.Recipe)
	for _, im := range f.Images {
		chunk(&b, TypeImage, im)
	}
	for _, ov := range f.Overlays {
		chunk(&b, TypeOverlay, ov)
	}
	if f.Raster != nil {
		chunk(&b, TypeRaster, f.Raster)
	}
	return b.Bytes()
}

func chunk(b *bytes.Buffer, t string, data []byte) {
	b.WriteString(t)
	var n [lengthLen]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(data)))
	b.Write(n[:])
	b.Write(data)
}

// NativePx is the frame's size in the pixels chgksuite sizes pictures by (120
// per inch), so an Edited Image lands in a question at the size of its frame,
// whatever resolution its raster was baked at.
func (f *File) NativePx() (w, h int) {
	toPx := func(mm float64) int { return max(1, int(math.Round(mm/mmPerInch*pxPerInch))) }
	return toPx(f.Bake.W), toPx(f.Bake.H)
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', coordDecimals, floatBits) }

var mimes = map[string]string{"png": "image/png", "jpg": "image/jpeg"}

// overlayBody is an overlay without its outer <svg> element. Every overlay is
// written with the frame as its viewBox, so its contents drop into the
// composed SVG unchanged.
func overlayBody(svg []byte) ([]byte, error) {
	s := string(svg)
	open := strings.Index(s, "<svg")
	end := strings.LastIndex(s, "</svg>")
	if open < 0 || end < 0 {
		return nil, ErrCorrupt
	}
	gt := strings.IndexByte(s[open:], '>')
	if gt < 0 || open+gt >= end {
		return nil, ErrCorrupt
	}
	return []byte(s[open+gt+1 : end]), nil
}

// VectorSVG is the vector rendition as one SVG the size of the frame: the
// background, every picture as an embedded image, every overlay inline, in
// stacking order. typst sets its text as real text in the PDF (it ships Noto
// Sans, which the overlays are set in). It exists only in memory, at export.
func (f *File) VectorSVG() ([]byte, error) {
	bk := f.Bake
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="%smm" height="%smm" viewBox="0 0 %s %s">`,
		num(bk.W), num(bk.H), num(bk.W), num(bk.H))
	fmt.Fprintf(&b, `<rect width="%s" height="%s" fill="%s"/>`, num(bk.W), num(bk.H), xmlAttr(bk.BG))
	for _, l := range bk.Layers {
		if err := f.writeLayer(&b, l); err != nil {
			return nil, err
		}
	}
	b.WriteString("</svg>")
	return []byte(b.String()), nil
}

func (f *File) writeLayer(b *strings.Builder, l Layer) error {
	if l.Kind == "image" {
		fmt.Fprintf(b, `<image x="%s" y="%s" width="%s" height="%s" preserveAspectRatio="none" xlink:href="data:%s;base64,%s"/>`,
			num(l.X), num(l.Y), num(l.W), num(l.H), mimes[l.Ext], base64.StdEncoding.EncodeToString(f.Images[l.Src]))
		return nil
	}
	body, err := overlayBody(f.Overlays[l.Over])
	if err != nil {
		return err
	}
	b.WriteString("<g>")
	b.Write(body)
	b.WriteString("</g>")
	return nil
}

func xmlAttr(s string) string {
	return strings.NewReplacer(`&`, "&amp;", `<`, "&lt;", `"`, "&quot;").Replace(s)
}
