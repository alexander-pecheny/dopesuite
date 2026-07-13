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
// Images are handed to typst under generated names (img1.png, img2.png, …): typst
// picks the decoder from the file extension, and xy's attachments are usually WebP
// named whatever the user named them.
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
	png, nativeW, nativeH, err := imgconv.ToPNG(raw)
	if err != nil {
		p.addStyled(missingImage(im.Name), "bold")
		return
	}
	name := fmt.Sprintf("img%d.png", len(e.used)+1)
	e.used[name] = png

	widthIn, heightIn := im.SizeInches(nativeW, nativeH)
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
