package pptx

import (
	"math"
	"regexp"
	"strings"

	"xy/internal/chgk/typo"
)

// This file is chgksuite's shrink-to-fit: lay the paragraphs out at the size
// they were given, and if they overflow the box, take a point off everything and
// try again. The wrapping is its own — PowerPoint's is not reachable from here —
// so it has to break lines the same way, including the URL rule.

// token is one piece of a line: a run's text, and whether it is a space that a
// break may fall on.
type token struct {
	run       *run
	text      string
	breakable bool
}

var (
	reHardBreak = regexp.MustCompile(`[\n\r\v]`)
	reSpaceRun  = regexp.MustCompile(`( +)`)
)

// urlBreakAfterChars are the characters a long URL may wrap after, which is what
// keeps one from forcing the whole box to shrink.
const urlBreakAfterChars = "/&?=-_.,;:+~#%"

// splitForWrapping is _split_runs_for_wrapping: the paragraph as explicit lines
// of tokens, split at hard breaks and at spaces.
func (e *exporter) splitForWrapping(p *paragraph) [][]token {
	lines := [][]token{{}}
	appendText := func(r *run, text string) {
		for i, line := range reHardBreak.Split(text, -1) {
			if i > 0 {
				lines = append(lines, nil)
			}
			for _, part := range splitKeepingSpaces(line) {
				if part == "" {
					continue
				}
				last := len(lines) - 1
				switch {
				case strings.TrimSpace(part) == "" && !strings.ContainsAny(part, " ‑"):
					lines[last] = append(lines[last], token{r, part, true})
				case canBreakLikeURL(r, part):
					lines[last] = appendURLTokens(lines[last], r, part)
				default:
					lines[last] = append(lines[last], token{r, part, false})
				}
			}
		}
	}
	for _, r := range p.runs {
		if r.br {
			lines = append(lines, nil)
			continue
		}
		appendText(r, r.text)
	}
	return lines
}

// splitKeepingSpaces is Python's re.split(r"( +)", …): the runs of spaces come
// back as pieces of their own, because a break falls on them and their width
// counts towards the line's.
func splitKeepingSpaces(line string) []string {
	var out []string
	last := 0
	for _, loc := range reSpaceRun.FindAllStringIndex(line, -1) {
		out = append(out, line[last:loc[0]], line[loc[0]:loc[1]])
		last = loc[1]
	}
	return append(out, line[last:])
}

func canBreakLikeURL(r *run, text string) bool {
	if r.hyperlink != "" {
		return true
	}
	return typo.HasURL(text)
}

func appendURLTokens(line []token, r *run, text string) []token {
	part := ""
	for _, ch := range text {
		part += string(ch)
		if strings.ContainsRune(urlBreakAfterChars, ch) {
			line = append(line, token{r, part, false}, token{r, "", true})
			part = ""
		}
	}
	if part != "" {
		line = append(line, token{r, part, false})
	}
	return line
}

// layoutLines is _paragraph_layout_lines: the paragraph broken to maxWidth. The
// second return is the widest token that cannot be broken, which decides whether
// anything could ever fit.
func (e *exporter) layoutLines(p *paragraph, maxWidth float64) (lines [][]token, widest float64, ok bool) {
	for _, explicit := range e.splitForWrapping(p) {
		if len(explicit) == 0 {
			lines = append(lines, nil)
			continue
		}
		var current []token
		var currentWidth float64
		var pending []token
		hasContent := false

		for _, t := range explicit {
			tokenWidth, measured := e.tokenWidth(p, []token{t})
			if !measured {
				return nil, 0, false
			}
			if t.breakable {
				pending = append(pending, t)
				continue
			}
			spaceWidth, measured := e.tokenWidth(p, pending)
			if !measured {
				return nil, 0, false
			}
			candidate := currentWidth + spaceWidth + tokenWidth
			widest = math.Max(widest, tokenWidth)
			if hasContent && candidate > maxWidth {
				lines = append(lines, current)
				current = []token{t}
				currentWidth = tokenWidth
			} else {
				current = append(current, pending...)
				current = append(current, t)
				currentWidth = candidate
			}
			pending = nil
			hasContent = true
		}
		lines = append(lines, current)
	}
	return lines, widest, true
}

func (e *exporter) tokenWidth(p *paragraph, tokens []token) (float64, bool) {
	total := 0.0
	for _, t := range tokens {
		w, ok := e.runTextWidth(t.run, p, t.text)
		if !ok {
			return 0, false
		}
		total += w
	}
	return total, true
}

func (e *exporter) effectiveParagraphSize(p *paragraph) float64 {
	if p.size > 0 {
		return p.size
	}
	return e.cfg.fontSize("default_size", 32)
}

func (e *exporter) effectiveRunSize(r *run, p *paragraph) float64 {
	if r.sizeSet && r.size > 0 {
		return r.size
	}
	return e.effectiveParagraphSize(p)
}

func (e *exporter) runTextWidth(r *run, p *paragraph, text string) (float64, bool) {
	if text == "" {
		return 0, true
	}
	face := e.faces.pick(r.bold, r.italic)
	if face == nil {
		return 0, false
	}
	return face.width(text, e.effectiveRunSize(r, p)), true
}

// lineHeight is _paragraph_line_height_px: the tallest run on the line, then
// whatever line spacing the paragraph asks for.
func (e *exporter) lineHeight(p *paragraph, line []token) float64 {
	var height, size float64
	withText := line[:0:0]
	for _, t := range line {
		if t.text != "" {
			withText = append(withText, t)
		}
	}
	if len(withText) > 0 {
		for _, t := range withText {
			face := e.faces.pick(t.run.bold, t.run.italic)
			if face == nil {
				continue
			}
			height = math.Max(height, face.lineHeight(e.effectiveRunSize(t.run, p)))
			size = math.Max(size, e.effectiveRunSize(t.run, p))
		}
	} else {
		size = e.effectiveParagraphSize(p)
		if face := e.faces.pick(false, false); face != nil {
			height = face.lineHeight(size)
		} else {
			height = ptToPx(size)
		}
	}
	switch {
	case p.lineSpacePt > 0:
		return ptToPx(p.lineSpacePt)
	case p.lineSpacing > 0:
		return height * p.lineSpacing
	}
	return height
}

func ptToPx(v float64) float64 { return v * pxPerInch / ptPerInch }
func emuToPx(v int64) float64  { return float64(v) / emuPerInch * pxPerInch }
func pxToEMU(v float64) int64  { return int64(v / pxPerInch * emuPerInch) }
func innerWidth(t *textbox) float64 {
	return emuToPx(max64(t.width-t.tf.marginLeft-t.tf.marginRight, 1))
}
func innerHeight(t *textbox) float64 {
	return emuToPx(max64(t.high-t.tf.marginTop-t.tf.marginBottom, 1))
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// fits is _text_frame_fits: every paragraph laid out, and the total no taller
// than the box. The 0.99 is chgksuite's own margin of error.
func (e *exporter) fits(t *textbox) bool {
	maxWidth := innerWidth(t) * 0.99
	maxHeight := innerHeight(t) * 0.99
	total := 0.0
	for _, p := range t.tf.paragraphs {
		lines, widest, ok := e.layoutLines(p, maxWidth)
		if !ok {
			return true
		}
		if widest > maxWidth {
			return false
		}
		for _, line := range lines {
			total += e.lineHeight(p, line)
		}
		total += ptToPx(p.spaceAfter) + ptToPx(p.spaceBefore)
	}
	return total <= maxHeight
}

// sizedItem is one thing the shrink pass resizes, remembering what it started at.
type sizedItem struct {
	paragraph *paragraph
	run       *run
	original  float64
}

func (e *exporter) collectSizes(t *textbox) []sizedItem {
	var items []sizedItem
	fallback := e.cfg.fontSize("default_size", 32)
	for _, p := range t.tf.paragraphs {
		size := p.size
		if size == 0 {
			size = fallback
		}
		items = append(items, sizedItem{paragraph: p, original: size})
		for _, r := range p.runs {
			runSize := size
			if r.sizeSet && r.size > 0 {
				runSize = r.size
			}
			items = append(items, sizedItem{run: r, original: runSize})
		}
	}
	return items
}

func setSizes(items []sizedItem, delta, minSize float64) {
	for _, it := range items {
		size := math.Max(minSize, it.original-delta)
		if it.paragraph != nil {
			it.paragraph.size = size
			continue
		}
		it.run.size, it.run.sizeSet = size, true
	}
}

// shrink is _custom_shrink_textbox: take a point off everything until it fits,
// down to the smallest size the config allows.
func (e *exporter) shrink(t *textbox, minSize float64) {
	if e.cfg.disableShrinkFit() || e.faces.empty() {
		return
	}
	items := e.collectSizes(t)
	if len(items) == 0 {
		return
	}
	if minSize == 0 {
		minSize = e.cfg.smallestSize()
	}
	largest := 0.0
	for _, it := range items {
		largest = math.Max(largest, it.original)
	}
	maxDelta := int(math.Max(0, math.Ceil(largest-minSize)))
	for delta := 0; delta <= maxDelta; delta++ {
		setSizes(items, float64(delta), minSize)
		if e.fits(t) {
			return
		}
	}
}

// handoutFontSizeForText is _get_handout_font_size_for_text: a handout is set as
// large as it can be without any of its lines wrapping.
func (e *exporter) handoutFontSizeForText(text string, t *textbox, minSize float64) float64 {
	maxSize := e.cfg.handoutFontSize()
	if minSize == 0 {
		minSize = e.cfg.fontSize("question_size", 32)
	}
	if maxSize <= minSize {
		return maxSize
	}
	face := e.faces.pick(false, false)
	if face == nil {
		return maxSize
	}
	lines := e.plainLinesForMeasurement(text)
	if len(lines) == 0 {
		return maxSize
	}
	maxWidth := innerWidth(t) * 0.99
	for size := maxSize; size > minSize; size-- {
		fitsAll := true
		for _, line := range lines {
			if face.width(line, size) > maxWidth {
				fitsAll = false
				break
			}
		}
		if fitsAll {
			return size
		}
	}
	return minSize
}
