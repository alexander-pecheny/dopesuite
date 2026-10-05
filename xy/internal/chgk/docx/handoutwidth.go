package docx

import (
	"math"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"xy/internal/chgk/handout"
	"xy/internal/chgk/inline"
)

// The handout box's width (handout_box_twips in chgksuite's docx.py). A typst
// grid sizes its column to the content by itself; Word does not, so a one-line
// handout is measured here, in Noto Sans, the template's face.
const (
	fullWidthTw       = 2 * cellWidth // the template's text width
	tableCellMarginTw = 108           // TableNormal's margin on either side of a cell's text
	twipsPerPt        = 20
	bodyPt            = 12
	handoutCaptionPt  = 10
	// Room for what a measurement cannot know: the frame itself, a bold run, a
	// face that is not quite Noto Sans.
	handoutSlackTw     = 120
	pointsPerInch      = 72
	notoSansUnitsPerEm = 1000
)

var (
	notoOnce sync.Once
	notoFont *sfnt.Font
)

// textWidthPt is the advance of text in Noto Sans Regular at size, in points:
// the font's own integer advances, unhinted and unkerned — what Pillow's BASIC
// layout reports at one pixel per font unit, so chgksuite gets the same number.
func textWidthPt(text string, size float64) float64 {
	notoOnce.Do(func() {
		f, err := sfnt.Parse(handout.RegularFont())
		if err != nil {
			panic(err) // embedded at build time
		}
		notoFont = f
	})
	var buf sfnt.Buffer
	units := 0
	for _, r := range text {
		idx, err := notoFont.GlyphIndex(&buf, r)
		if err != nil {
			idx = 0
		}
		adv, err := notoFont.GlyphAdvance(&buf, idx, fixed.I(notoSansUnitsPerEm), font.HintingNone)
		if err != nil {
			continue
		}
		units += adv.Round()
	}
	return float64(units) * size / notoSansUnitsPerEm
}

// handoutLineWidthPt is how wide a handout that is one line is set; ok is false
// when it is not one line: a break, a list, a block picture with anything beside
// it, or a picture that cannot be read.
func (e *exporter) handoutLineWidthPt(v any) (float64, bool) {
	s, isString := v.(string)
	if !isString {
		return 0, false
	}
	blocks, hasText, width := 0, false, 0.0
	for _, r := range inline.Parse4sElem(inline.BacktickReplace(inline.ReplaceEscaped(s))) {
		switch r.Kind {
		case "linebreak", "pagebreak":
			return 0, false
		case "img":
			im, w, _, ok := e.imageInches(r.Text)
			if !ok {
				return 0, false
			}
			if !im.Inline {
				blocks++
			}
			width += w * pointsPerInch
			continue
		}
		text := r.Text
		if r.Kind == "screen" {
			text = r.ForPrint
		}
		if strings.Contains(text, "\n") {
			return 0, false
		}
		hasText = hasText || strings.TrimSpace(text) != ""
		width += textWidthPt(text, bodyPt)
	}
	// A block picture sits on a line of its own, so it is one line only alone.
	if blocks > 1 || (blocks > 0 && hasText) {
		return 0, false
	}
	return width, true
}

// handoutBoxTw is the handout box's width when the handout is one line and the
// box can be narrower than the page: wide enough for that line or for the
// caption, whichever is wider. 0 for a box the whole width of the page.
func (e *exporter) handoutBoxTw(label string, v any) int {
	line, ok := e.handoutLineWidthPt(v)
	if !ok {
		return 0
	}
	content := int(math.Ceil(line*twipsPerPt)) + 2*tableCellMarginTw
	caption := int(math.Ceil(textWidthPt(label, handoutCaptionPt) * twipsPerPt))
	width := max(content, caption) + handoutSlackTw
	if width >= fullWidthTw {
		return 0
	}
	return width
}
