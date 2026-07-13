package docx

import (
	"fmt"
	"math"
	"strings"

	"xy/internal/chgk/imgconv"
	"xy/internal/chgk/inline"
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
	im, ok := inline.ParseImg(arg)
	if !ok {
		return ""
	}
	name := im.Name

	raw := e.images[name]
	if raw == nil {
		if base := name[strings.LastIndexAny(name, `/\`)+1:]; base != name {
			raw = e.images[base]
		}
	}
	if raw == nil {
		return missingImage(name)
	}
	pngData, nativeW, nativeH, err := imgconv.ToPNG(raw)
	if err != nil {
		return missingImage(name)
	}

	widthIn, heightIn := im.SizeInches(nativeW, nativeH)
	cx, cy := inchesToEMU(widthIn), inchesToEMU(heightIn)

	idx := len(e.media) + 1
	relID := fmt.Sprintf("rId%d", e.nextRel)
	e.nextRel++
	partName := fmt.Sprintf("media/image%d.png", idx)
	e.media = append(e.media, mediaItem{relID: relID, partName: partName, data: pngData})
	e.rels = append(e.rels, relItem{id: relID, typ: imageRelType, target: partName})
	docID := e.nextDoc
	e.nextDoc++

	drawing := drawingXML(relID, docID, cx, cy, name)
	if im.Inline {
		return drawing
	}
	// block image: surround with line breaks so it sits on its own line
	return brk() + drawing + brk()
}

func inchesToEMU(in float64) int64 { return int64(math.Round(in * emuPerInch)) }

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
