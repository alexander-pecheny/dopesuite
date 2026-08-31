package pptx

import (
	"math"
	"strings"

	"xy/internal/chgk/imgconv"
	"xy/internal/chgk/inline"
)

// A picture on a slide is either the question's own — laid beside or above the
// text, taking a third of the box unless it is marked big — or an inline one,
// which is drawn where its placeholder of non-breaking spaces ended up.

// slideImage is chgksuite's parsed image dict: a size in inches, and the two
// flags that decide where it goes.
type slideImage struct {
	name          string
	data          []byte
	width, height float64 // inches
	big, inline   bool
	handout       bool
}

// parseImage is parseimg with dimensions="inches": the size the directive asks
// for, or the picture's own, clamped and read at chgksuite's 120 dpi.
// inline.Img.SizeInches is that arithmetic, shared with the .docx and the PDF —
// but its own inline rule is the exporters', not this one's, so it is asked for
// the ordinary size and _normalize_inline_image_size shrinks it after.
func (e *exporter) parseImage(arg string) (*slideImage, bool) {
	img, ok := inline.ParseImg(arg)
	if !ok {
		return nil, false
	}
	data, found := e.images[img.Name]
	if !found {
		return nil, false
	}
	decoded, err := imgconv.Decode(data)
	if err != nil {
		return nil, false
	}
	b := decoded.Bounds()
	sized := img
	sized.Inline = false
	w, h := sized.SizeInches(b.Dx(), b.Dy())
	return &slideImage{
		name: img.Name, data: data, width: w, height: h,
		big: img.Big, inline: img.Inline,
	}, true
}

// imageFrom is _get_image_from_4s: the first picture a field names that is not
// an inline one, scaled if it is the handout.
func (e *exporter) imageFrom(v any) *slideImage {
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			if img := e.imageFrom(x); img != nil {
				return img
			}
		}
	case string:
		handoutMatch := e.handoutRe1.FindString(t)
		for _, r := range inline.Parse4sElem(t) {
			if r.Kind != "img" {
				continue
			}
			img, ok := e.parseImage(r.Text)
			if !ok || img.inline {
				continue
			}
			img.handout = handoutMatch != "" && strings.Contains(handoutMatch, r.Text)
			return e.scaleHandoutImage(img)
		}
	}
	return nil
}

func (e *exporter) scaleHandoutImage(img *slideImage) *slideImage {
	if !img.handout {
		return img
	}
	scale := e.cfg.handoutImageScale()
	if scale == 0 || scale == 1 {
		return img
	}
	img.width *= scale
	img.height *= scale
	return img
}

// handoutFrom is _get_handout_from_4s: the handout's text, wherever in the
// question it is written.
func (e *exporter) handoutFrom(v any) string {
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			if h := e.handoutFrom(x); h != "" {
				return h
			}
		}
	case string:
		if m := e.handoutRe1.FindStringSubmatch(t); m != nil {
			if e.cfg.includeHandoutLabel() {
				return m[0]
			}
			return m[1]
		}
		for _, line := range strings.Split(t, "\n") {
			if m := e.handoutRe2.FindStringSubmatch(line); m != nil {
				if e.cfg.includeHandoutLabel() {
					return m[0]
				}
				return m[1]
			}
		}
	}
	return ""
}

// splitHandoutFromText is _split_handout_from_text: the handout taken out of the
// question, so the two can be set at different sizes.
func (e *exporter) splitHandoutFromText(v any) (string, any) {
	t, ok := v.(string)
	if !ok {
		return "", v
	}
	if loc := e.handoutRe1.FindStringSubmatchIndex(t); loc != nil {
		handout := t[loc[2]:loc[3]]
		if e.cfg.includeHandoutLabel() {
			handout = t[loc[0]:loc[1]]
		}
		return strings.TrimSpace(handout), strings.TrimSpace(t[:loc[0]] + t[loc[1]:])
	}
	lines := strings.Split(t, "\n")
	for i, line := range lines {
		m := e.handoutRe2.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		handout := m[1]
		if e.cfg.includeHandoutLabel() {
			handout = line
		}
		rest := append(append([]string{}, lines[:i]...), lines[i+1:]...)
		return strings.TrimSpace(handout), strings.TrimSpace(strings.Join(rest, "\n"))
	}
	return "", v
}

// makeSlideLayout is make_slide_layout: where the picture goes and what is left
// of the box for the text.
func (e *exporter) makeSlideLayout(image *slideImage, s *slidePart, allowBigImage bool) *textbox {
	if image == nil {
		return e.textboxAt(s)
	}
	t := e.cfg.textbox()
	baseLeft, baseTop := inches(tableNumOr(t, "left", 0)), inches(tableNumOr(t, "top", 0))
	baseWidth, baseHigh := inches(tableNumOr(t, "width", 0)), inches(tableNumOr(t, "height", 0))
	imgWidth, imgHigh := inches(image.width), inches(image.height)
	spaceAfter := int64(0)
	if image.handout {
		spaceAfter = points(e.cfg.handoutSpaceAfter())
	}

	if e.cfg.overlayImageAndText() {
		s.addPicture(e.pkg, image.name, image.data, baseLeft, baseTop, imgWidth, imgHigh)
		return e.textboxAt(s)
	}
	if e.cfg.disableAutolayout() {
		s.addPicture(e.pkg, image.name, image.data, baseLeft, baseTop, imgWidth, imgHigh)
		if image.width/image.height < 1 {
			return s.addTextbox(baseLeft+imgWidth+spaceAfter, baseTop,
				max64(baseWidth-imgWidth-spaceAfter, 0), baseHigh)
		}
		return s.addTextbox(baseLeft, baseTop+imgHigh+spaceAfter,
			baseWidth, max64(baseHigh-imgHigh-spaceAfter, 0))
	}

	bigMode := image.big && !e.cfg.textIsDuplicated() && allowBigImage
	var left, top, width, high, imgLeft, imgTop int64
	if image.width/image.height < 1 { // a tall picture stands beside the text
		maxWidth := baseWidth / 3
		if bigMode {
			maxWidth *= 2
		}
		if image.handout {
			maxWidth = int64(float64(maxWidth) * e.cfg.handoutImageScale())
		}
		maxWidth = min64(maxWidth, baseWidth-spaceAfter)
		if imgWidth > maxWidth || bigMode {
			imgHigh = int64(float64(imgHigh) * (float64(maxWidth) / float64(imgWidth)))
			imgWidth = maxWidth
		}
		left, top = baseLeft+imgWidth+spaceAfter, baseTop
		width, high = max64(baseWidth-imgWidth-spaceAfter, 0), baseHigh
		imgLeft = baseLeft
		imgTop = baseTop + int64(0.5*float64(baseHigh-imgHigh))
	} else { // a wide one sits above it
		maxHigh := baseHigh / 3
		if bigMode {
			maxHigh *= 2
		}
		if image.handout {
			maxHigh = int64(float64(maxHigh) * e.cfg.handoutImageScale())
		}
		maxHigh = min64(maxHigh, baseHigh-spaceAfter)
		if imgHigh > maxHigh || bigMode {
			imgWidth = int64(float64(imgWidth) * (float64(maxHigh) / float64(imgHigh)))
			imgHigh = maxHigh
		}
		left, top = baseLeft, baseTop+imgHigh+spaceAfter
		width, high = baseWidth, max64(baseHigh-imgHigh-spaceAfter, 0)
		imgTop = baseTop
		imgLeft = baseLeft + int64(0.5*float64(baseWidth-imgWidth))
	}
	s.addPicture(e.pkg, image.name, image.data, imgLeft, imgTop, imgWidth, imgHigh)
	return s.addTextbox(left, top, width, high)
}

// addSlideWithImage is add_slide_with_image: the picture alone, as large as the
// box allows.
func (e *exporter) addSlideWithImage(image *slideImage, number string) {
	s := e.pkg.addSlide(e.questionLayout)
	if number != "" {
		e.setQuestionNumber(s, number)
	}
	t := e.cfg.textbox()
	baseLeft, baseTop := inches(tableNumOr(t, "left", 0)), inches(tableNumOr(t, "top", 0))
	baseWidth, baseHigh := inches(tableNumOr(t, "width", 0)), inches(tableNumOr(t, "height", 0))
	imgWidth, imgHigh := inches(image.width), inches(image.height)
	if image.big || imgWidth > baseWidth {
		imgHigh = int64(float64(imgHigh) * (float64(baseWidth) / float64(imgWidth)))
		imgWidth = baseWidth
	}
	if imgHigh > baseHigh {
		imgWidth = int64(float64(imgWidth) * (float64(baseHigh) / float64(imgHigh)))
		imgHigh = baseHigh
	}
	s.addPicture(e.pkg, image.name, image.data,
		baseLeft+int64(0.5*float64(baseWidth-imgWidth)),
		baseTop+int64(0.5*float64(baseHigh-imgHigh)),
		imgWidth, imgHigh)
}

// ── inline images ───────────────────────────────────────────────────────────

// pendingImage is an inline picture waiting for the text around it to settle: it
// is drawn where the placeholder it stands in ends up, which is only known once
// the box has been shrunk to fit.
type pendingImage struct {
	paragraph   *paragraph
	placeholder string
	image       *slideImage
	slide       *slidePart
	placed      bool
}

// normalizeInlineSize is _normalize_inline_image_size: an inline picture with no
// size of its own is a sixth of an inch tall, which is about a line.
func normalizeInlineSize(image *slideImage, spec string) *slideImage {
	if reSizedImage.MatchString(spec) {
		return image
	}
	out := *image
	const height = 1.0 / 6
	out.width = image.width * (height / image.height)
	out.height = height
	return &out
}

// addInlineImage is _add_inline_image: a run of non-breaking spaces as wide as
// the picture, which the placement pass then finds.
func (e *exporter) addInlineImage(p *paragraph, s *slidePart, image *slideImage, spec string) {
	image = normalizeInlineSize(image, spec)
	placeholder := e.inlinePlaceholder(p, image.width)
	e.addRun(p, placeholder)
	e.pendingImages = append(e.pendingImages, &pendingImage{
		paragraph: p, placeholder: placeholder, image: image, slide: s,
	})
}

func (e *exporter) inlinePlaceholder(p *paragraph, width float64) string {
	size := e.effectiveParagraphSize(p)
	spaceWidth := 0.0
	if face := e.faces.pick(false, false); face != nil {
		spaceWidth = face.width(" ", size)
	}
	if spaceWidth == 0 {
		spaceWidth = ptToPx(size) * 0.35
	}
	return strings.Repeat(" ", max(1, roundHalf(width*pxPerInch/math.Max(spaceWidth, 1))))
}

// placeInlineImages is _place_inline_images: walk the laid-out lines, and where
// a placeholder sits, put its picture.
func (e *exporter) placeInlineImages(t *textbox, s *slidePart) {
	var mine []*pendingImage
	var rest []*pendingImage
	for _, entry := range e.pendingImages {
		if entry.slide == s {
			mine = append(mine, entry)
		} else {
			rest = append(rest, entry)
		}
	}
	if len(mine) == 0 {
		return
	}
	e.pendingImages = rest

	originX := emuToPx(t.tf.marginLeft)
	y := emuToPx(t.tf.marginTop)
	maxWidth := innerWidth(t) * 0.99

	for _, p := range t.tf.paragraphs {
		y += ptToPx(p.spaceBefore)
		lines, _, ok := e.layoutLines(p, maxWidth)
		if !ok {
			lines = e.fallbackLines(p)
		}
		for _, line := range lines {
			x := originX
			lineHeight := e.lineHeight(p, line)
			for _, tok := range line {
				for _, entry := range mine {
					if entry.placed || entry.paragraph != p || entry.placeholder != tok.text {
						continue
					}
					entry.placed = true
					imageHeightPx := entry.image.height * pxPerInch
					s.addPicture(e.pkg, entry.image.name, entry.image.data,
						t.left+pxToEMU(x),
						t.top+pxToEMU(y+math.Max(0, (lineHeight-imageHeightPx)/2)),
						inches(entry.image.width), inches(entry.image.height))
				}
				x += e.tokenWidthOrEstimate(p, tok)
			}
			y += lineHeight
		}
		y += ptToPx(p.spaceAfter)
	}
}

func (e *exporter) fallbackLines(p *paragraph) [][]token {
	return e.splitForWrapping(p)
}

// tokenWidthOrEstimate is _token_width_px_or_estimate: without a font to measure
// with, chgksuite guesses a character is a third of its size wide.
func (e *exporter) tokenWidthOrEstimate(p *paragraph, t token) float64 {
	if w, ok := e.runTextWidth(t.run, p, t.text); ok {
		return w
	}
	return float64(len([]rune(t.text))) * ptToPx(e.effectiveRunSize(t.run, p)) * 0.35
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
