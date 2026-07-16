package typstdoc

import (
	"fmt"
	"strings"

	"xy/internal/chgk/imgconv"
	"xy/internal/chgk/inline"
)

// addImage resolves an (img …) directive and appends the picture to the paragraph,
// at the size the docx export would give it (inline.Img.SizeInches — 120 dpi pixels
// through chgksuite's proportional_resize). A missing or undecodable image degrades
// to a bold "[нет изображения: …]", as in the docx, so an export never fails on one.
//
// The bytes are re-encoded for the size they are drawn at (imgconv.ForExport: downscale
// to 200 dpi, JPEG unless the image has transparency). Embedding the original is how
// an 800 KB photo turned into a megabyte PDF — a package is read on a screen and
// printed on A4, and nothing beyond that sampling is ever visible.
//
// Images are handed to typst under generated names (img1.jpg, img2.png, …): typst
// picks the decoder from the file extension, and xy's attachments are named whatever
// the user named them (and are usually WebP, which Word can't read either).
func (e *exporter) addImage(p *para, arg string) {
	im, ok := inline.ParseImg(arg)
	if !ok {
		return
	}
	raw := e.images[im.Name]
	if raw == nil {
		if base := im.Name[strings.LastIndexAny(im.Name, `/\`)+1:]; base != im.Name {
			raw = e.images[base]
		}
	}
	if raw == nil {
		p.addStyled(missingImage(im.Name), "bold")
		return
	}
	// The size it will be drawn at comes from the ORIGINAL pixel dimensions
	// (chgksuite's proportional_resize works off those), and the encoding is then
	// chosen for that size — so decode first, size second, encode third.
	src, err := imgconv.Decode(raw)
	if err != nil {
		p.addStyled(missingImage(im.Name), "bold")
		return
	}
	b := src.Bounds()
	widthIn, heightIn := im.SizeInches(b.Dx(), b.Dy())

	// The board preview caps a picture at 12em tall / 480px wide (.pv-img), but
	// SizeInches only clamps the AUTO size (longest side ≤ 5in) — a portrait photo
	// or an explicit h= still ate half a page of the PDF. Scale down (never up)
	// into the preview's box: 2in tall (12em at the 12pt body), 5in wide.
	const maxImgW, maxImgH = 5.0, 2.0
	if s := min(maxImgW/widthIn, maxImgH/heightIn); s < 1 {
		widthIn, heightIn = widthIn*s, heightIn*s
	}

	data, ext, err := imgconv.ForExport(raw, widthIn, heightIn)
	if err != nil {
		p.addStyled(missingImage(im.Name), "bold")
		return
	}
	name := fmt.Sprintf("img%d.%s", len(e.used)+1, ext)
	e.used[name] = data

	expr := fmt.Sprintf("box(image(%s, width: %s, height: %s))",
		typstString(name), mm(widthIn*25.4), mm(heightIn*25.4))
	if im.Inline {
		p.add(expr)
		return
	}
	// block image: on its own line, as the docx export puts it
	p.addBreak()
	p.add(expr)
	p.addBreak()
}

func missingImage(name string) string { return "[нет изображения: " + name + "]" }
