package docx

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"strconv"
	"strings"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// mediaItem is an image embedded into the docx (word/media/imageN.png + a
// relationship). All images are re-encoded to PNG so Word can display them
// (WebP — what xy often stores — isn't a valid docx image type).
type mediaItem struct {
	relID    string
	partName string // e.g. media/image1.png
	data     []byte // PNG bytes
}

const emuPerInch = 914400

// embedImage parses an (img …) directive's argument (the last whitespace token is
// the filename; the rest are big/inline/w=/h= options — chgksuite parseimg),
// re-encodes the referenced image to PNG, registers it, and returns the inline
// <w:drawing> run XML. Missing/undecodable images degrade to a bold
// "MISSING IMAGE …" run so export never fails.
func (e *exporter) embedImage(arg string) string {
	fields := strings.Fields(arg)
	if len(fields) == 0 {
		return ""
	}
	name := fields[len(fields)-1]
	opts := fields[:len(fields)-1]

	width, height := -1.0, -1.0
	big, inline := false, false
	for _, o := range opts {
		switch {
		case o == "big":
			big = true
		case o == "inline":
			inline = true
		case strings.HasPrefix(o, "w="):
			width = parseSingleSize(o[2:])
		case strings.HasPrefix(o, "h="):
			height = parseSingleSize(o[2:])
		case strings.HasPrefix(o, "inline="):
			inline = o[7:] == "1" || strings.EqualFold(o[7:], "true") || strings.EqualFold(o[7:], "yes")
		}
	}
	_ = big

	raw := e.images[name]
	if raw == nil {
		if base := name[strings.LastIndexAny(name, `/\`)+1:]; base != name {
			raw = e.images[base]
		}
	}
	if raw == nil {
		return missingImage(name)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return missingImage(name)
	}
	pngData, derr := toPNG(raw)
	if derr != nil {
		return missingImage(name)
	}

	cx, cy := imageEMU(cfg.Width, cfg.Height, width, height, inline)

	idx := len(e.media) + 1
	relID := fmt.Sprintf("rId%d", e.nextRel)
	e.nextRel++
	partName := fmt.Sprintf("media/image%d.png", idx)
	e.media = append(e.media, mediaItem{relID: relID, partName: partName, data: pngData})
	e.rels = append(e.rels, relItem{id: relID, typ: imageRelType, target: partName})
	docID := e.nextDoc
	e.nextDoc++

	drawing := drawingXML(relID, docID, cx, cy, name)
	if inline {
		return drawing
	}
	// block image: surround with line breaks so it sits on its own line
	return brk() + drawing + brk()
}

// imageEMU computes the drawing extent (EMU) mirroring chgksuite parseimg +
// add_picture (Inches at 120 dpi; proportional_resize for the no-dimension case).
func imageEMU(nativeW, nativeH int, width, height float64, inline bool) (int64, int64) {
	if nativeW <= 0 || nativeH <= 0 {
		nativeW, nativeH = 1, 1
	}
	if inline {
		heightIn := 1.0 / 6
		widthIn := heightIn * float64(nativeW) / float64(nativeH)
		return inchesToEMU(widthIn), inchesToEMU(heightIn)
	}
	rw, rh := proportionalResize(nativeW, nativeH)
	var widthIn, heightIn float64
	if width == -1 && height == -1 {
		widthIn = float64(rw) / 120
		heightIn = float64(rh) / 120
	} else {
		if width != -1 && height == -1 {
			height = float64(rh) * (width / float64(rw))
		} else if width == -1 && height != -1 {
			width = float64(rw) * (height / float64(rh))
		}
		widthIn = width / 120
		heightIn = height / 120
	}
	return inchesToEMU(widthIn), inchesToEMU(heightIn)
}

func inchesToEMU(in float64) int64 { return int64(math.Round(in * emuPerInch)) }

// proportionalResize mirrors chgksuite: clamp the longest side into [200, 600] px.
func proportionalResize(w, h int) (int, int) {
	mx := w
	if h > mx {
		mx = h
	}
	if mx > 600 {
		return w * 600 / mx, h * 600 / mx
	}
	if mx < 200 {
		return w * 200 / mx, h * 200 / mx
	}
	return w, h
}

// parseSingleSize mirrors chgksuite parse_single_size (px default; in→×120; em→×25).
func parseSingleSize(s string) float64 {
	switch {
	case strings.HasSuffix(s, "in"):
		v, _ := strconv.ParseFloat(s[:len(s)-2], 64)
		return v * 120
	case strings.HasSuffix(s, "em"):
		v, _ := strconv.ParseFloat(s[:len(s)-2], 64)
		return v * 25
	case strings.HasSuffix(s, "px"):
		s = s[:len(s)-2]
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func toPNG(raw []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func missingImage(name string) string {
	return "<w:r><w:rPr><w:b/></w:rPr>" + `<w:t xml:space="preserve">` + xmlEscape("[нет изображения: "+name+"]") + "</w:t></w:r>"
}

// drawingXML builds an inline picture run. xmlns:wp and xmlns:r are declared on
// the document root (template); xmlns:a / xmlns:pic are declared inline here.
func drawingXML(relID string, docID int, cx, cy int64, name string) string {
	const aNS = "http://schemas.openxmlformats.org/drawingml/2006/main"
	const picNS = "http://schemas.openxmlformats.org/drawingml/2006/picture"
	return fmt.Sprintf(`<w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0">`+
		`<wp:extent cx="%d" cy="%d"/><wp:effectExtent l="0" t="0" r="0" b="0"/>`+
		`<wp:docPr id="%d" name="Picture %d"/>`+
		`<wp:cNvGraphicFramePr><a:graphicFrameLocks xmlns:a="%s" noChangeAspect="1"/></wp:cNvGraphicFramePr>`+
		`<a:graphic xmlns:a="%s"><a:graphicData uri="%s">`+
		`<pic:pic xmlns:pic="%s"><pic:nvPicPr><pic:cNvPr id="%d" name="%s"/><pic:cNvPicPr/></pic:nvPicPr>`+
		`<pic:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill>`+
		`<pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm>`+
		`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic>`+
		`</a:graphicData></a:graphic></wp:inline></w:drawing></w:r>`,
		cx, cy, docID, docID, aNS, aNS, picNS, picNS, docID, xmlEscape(name), relID, cx, cy)
}
