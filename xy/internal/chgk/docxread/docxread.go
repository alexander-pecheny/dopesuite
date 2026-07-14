// Package docxread is a Go port of chgksuite's "docx → plain text" converter
// (chgksuite/parsing_engine.py's python_docx engine plus parser.docx_to_text's
// python_docx branch). It turns a .docx into the flat text the 4s parser eats,
// so xy can import Word packets in-process instead of shelling out to Python.
//
// Only the chgk game is in scope, which pins four of chgksuite's knobs:
// inject_heading_markers=False, preserve_ol_start=False, links="unwrap" and
// no_image_prefix=False. Only preserve_formatting still varies.
//
// python-docx has no Go equivalent, so opc.go/styles.go reimplement the parts of
// it the converter leans on (block iteration in document order, run formatting,
// style + numbering resolution, the table cell grid, image relationships).
package docxread

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"xy/internal/chgk/typo"
)

// Image is one image extracted from the document. Unlike chgksuite (which writes
// them next to the source file), we hand them back in memory.
type Image struct {
	Name string // the generated name, e.g. "myfile_001.jpeg"
	Data []byte
}

// Options mirrors the chgksuite args the python_docx engine reads.
type Options struct {
	// PreserveFormatting wraps bold/italic/underline runs in 4s "_" markers.
	PreserveFormatting bool
	// ImagePrefix is docx_to_text's bn_for_img: the source file's basename with
	// spaces replaced by underscores, plus a trailing "_".
	ImagePrefix string
}

// imageExtensions is parsing_engine._IMAGE_EXTENSIONS — note that image/jpg and
// image/jpeg both normalize to "jpeg", so an image1.jpg part comes out as .jpeg.
var imageExtensions = map[string]string{
	"image/bmp":     "bmp",
	"image/emf":     "emf",
	"image/gif":     "gif",
	"image/jpeg":    "jpeg",
	"image/jpg":     "jpeg",
	"image/png":     "png",
	"image/svg+xml": "svg",
	"image/tiff":    "tiff",
	"image/webp":    "webp",
	"image/wmf":     "wmf",
}

type converter struct {
	pkg    *pkg
	styles *styles
	num    *numbering
	opts   Options

	listCounters map[[2]string]int
	images       []Image
	// imgCounters is _generate_imgname's "first free NNN" search. Python probes
	// the filesystem, so the counter is per-extension: two images with different
	// extensions can both end up 001.
	imgCounters map[string]int
}

// ToText converts a .docx (raw bytes) to chgksuite's plain-text form, returning
// the images it found instead of writing them to disk.
func ToText(docx []byte, opts Options) (string, []Image, error) {
	p, err := openPkg(docx)
	if err != nil {
		return "", nil, err
	}
	doc := p.xmlPart("/word/document.xml")
	if doc == nil {
		return "", nil, fmt.Errorf("docxread: no word/document.xml")
	}
	body := doc.find("body")
	if body == nil {
		return "", nil, fmt.Errorf("docxread: no w:body")
	}
	c := &converter{
		pkg:          p,
		styles:       loadStyles(p),
		num:          loadNumbering(p),
		opts:         opts,
		listCounters: map[[2]string]int{},
		imgCounters:  map[string]int{},
	}
	txt := c.convert(body)

	// docx_to_text's common tail. The "\\-"/"\\." replacements are pandoc
	// leftovers, kept because they run for every engine. They are applied one
	// full pass at a time, as in Python — a single simultaneous pass would not
	// be identical.
	for _, rep := range [...][2]string{
		{"\\-", ""}, {"\\.", "."},
		{"( ", "("}, {"[ ", "["}, {" )", ")"}, {" ]", "]"}, {" :", ":"},
		{"&lt;", "<"}, {"&gt;", ">"}, {"&amp;", "&"},
	} {
		txt = strings.ReplaceAll(txt, rep[0], rep[1])
	}
	txt = reBadItalic.ReplaceAllString(txt, "") // fix bad italic from Word
	return normalizeDocxSpacing(txt), c.images, nil
}

var reBadItalic = regexp.MustCompile(`_ +_`)

// convert is _DocxTextConverter.convert: body blocks joined by a blank line.
func (c *converter) convert(body *node) string {
	var blocks []string
	for _, block := range iterBlocks(body) {
		blocks = append(blocks, c.blockText(block))
	}
	return strings.Join(blocks, "\n\n")
}

func (c *converter) blockText(block *node) string {
	if block.is("p") {
		return c.paragraphText(block)
	}
	return c.tableText(block)
}

// iterBlocks is _iter_blocks: direct w:p / w:tbl children, in document order.
func iterBlocks(parent *node) []*node {
	var out []*node
	for _, k := range parent.kids {
		if k.is("p") || k.is("tbl") {
			out = append(out, k)
		}
	}
	return out
}

func (c *converter) paragraphText(p *node) string {
	text := c.paragraphInlineText(p)
	// Note the explicit strip set: an NBSP-only paragraph is *not* empty here.
	if strings.Trim(text, " \t\r\n") == "" {
		c.breakListIfNeeded()
		return ""
	}
	// _heading_level only ever feeds the $$H1$$ markers, which chgk never asks
	// for (inject_heading_markers=False), so it is not ported.
	if prefix := c.listPrefix(p); prefix != "" {
		text = prefix + text
	}
	return text
}

// ── inline text ─────────────────────────────────────────────────────────────

func (c *converter) paragraphInlineText(p *node) string {
	var b strings.Builder
	for _, child := range p.kids {
		switch {
		case child.is("r"):
			b.WriteString(c.runText(child, false, false))
		case child.is("hyperlink"):
			b.WriteString(c.hyperlinkText(child))
		case child.is("del"): // a tracked deletion contributes nothing
		default:
			b.WriteString(c.containerText(child))
		}
	}
	return b.String()
}

// containerText is _container_text: anything that is not a run or a hyperlink
// (w:ins, w:smartTag, w:sdt, …) is descended into looking for runs.
func (c *converter) containerText(container *node) string {
	var b strings.Builder
	for _, child := range container.kids {
		switch {
		case child.is("r"):
			b.WriteString(c.runText(child, false, false))
		case child.is("hyperlink"):
			b.WriteString(c.hyperlinkText(child))
		case child.is("del"):
		default:
			b.WriteString(c.containerText(child))
		}
	}
	return b.String()
}

func (c *converter) hyperlinkText(hyperlink *node) string {
	var rendered, plainB strings.Builder
	for _, child := range hyperlink.kids {
		if !child.is("r") {
			continue
		}
		rendered.WriteString(c.runText(child, false, true))
		plainB.WriteString(c.runText(child, true, false))
	}
	out := rendered.String()
	plain := typo.REW(plainB.String())
	href := c.hyperlinkHref(hyperlink)
	if href == "" {
		return out
	}
	// links="unwrap": keep the anchor text, and append the href only when the
	// text does not already show the URL.
	if strings.HasPrefix(plain, "http") {
		return out
	}
	stripped := strings.TrimFunc(plain, unicode.IsSpace)
	if strings.HasPrefix(href, "http") &&
		!strings.Contains(href, stripped) &&
		!strings.Contains(unquote(href), unquote(stripped)) {
		return out + " (" + href + ")"
	}
	return out
}

func (c *converter) hyperlinkHref(hyperlink *node) string {
	rID, ok := hyperlink.attr(nsR, "id")
	if !ok || rID == "" {
		return ""
	}
	rel, ok := c.pkg.rels[rID]
	if !ok {
		return ""
	}
	return rel.target // rel.target_ref — the raw Target, not resolved
}

func (c *converter) runText(run *node, plain, suppressUnderline bool) string {
	preserve := false
	var bold, italic, underline bool
	if !plain {
		preserve = c.opts.PreserveFormatting
		bold, italic, underline = runFormatting(run)
		if suppressUnderline {
			underline = false
		}
	}
	var chunks strings.Builder
	var buffer strings.Builder
	flush := func() {
		if buffer.Len() == 0 {
			return
		}
		text := buffer.String()
		buffer.Reset()
		if plain {
			chunks.WriteString(text)
		} else {
			chunks.WriteString(renderText(text, bold, italic, underline, preserve))
		}
	}
	for _, child := range run.kids {
		switch {
		case child.is("t"):
			buffer.WriteString(child.text)
		case child.is("tab"):
			buffer.WriteString("\t")
		case child.is("br"), child.is("cr"):
			buffer.WriteString("\n")
		case child.is("noBreakHyphen"):
			buffer.WriteString("-")
		case child.is("softHyphen"):
		case child.is("drawing"), child.is("pict"):
			flush()
			if !plain {
				for _, m := range c.imageMarkers(child) {
					chunks.WriteString(m)
				}
			}
		}
	}
	flush()
	return chunks.String()
}

// runFormatting is _run_formatting: only direct w:rPr toggles count — python-docx
// does not resolve character styles here, and a missing toggle means "off".
func runFormatting(run *node) (bold, italic, underline bool) {
	rPr := run.find("rPr")
	if rPr == nil {
		return false, false, false
	}
	toggle := func(local string) bool {
		el := rPr.find(local)
		if el == nil {
			return false
		}
		val, ok := el.wattr("val")
		if !ok {
			return true
		}
		switch val {
		case "0", "false", "False", "off":
			return false
		case "none": // w:u val="none" is python-docx's False
			return local != "u"
		}
		return true
	}
	return toggle("b"), toggle("i"), toggle("u")
}

// formatMarker is _format_marker: the 4s markup for a run's combined emphasis.
func formatMarker(bold, italic, underline bool) string {
	switch {
	case italic && bold && underline:
		return strings.Repeat("_", 6)
	case bold && underline:
		return strings.Repeat("_", 5)
	case bold && italic:
		return strings.Repeat("_", 4)
	case underline:
		return strings.Repeat("_", 3)
	case bold:
		return "__"
	case italic:
		return "_"
	}
	return ""
}

func renderText(text string, bold, italic, underline, preserve bool) string {
	if text == "" {
		return ""
	}
	text = typo.EscapeUnderscoresExceptURLs(text, false)
	if !preserve {
		return text
	}
	marker := formatMarker(bold, italic, underline)
	if marker == "" || strings.TrimFunc(text, unicode.IsSpace) == "" {
		return text
	}
	// ^(\s*)(.*?)(\s*)$ with DOTALL — i.e. keep the whitespace outside the marks.
	body := strings.TrimFunc(text, unicode.IsSpace)
	i := strings.Index(text, body)
	return text[:i] + marker + body + marker + text[i+len(body):]
}

// ── images ──────────────────────────────────────────────────────────────────

func (c *converter) imageMarkers(element *node) []string {
	var rIDs []string
	for _, blip := range element.descendants(nsA, "blip") {
		if rID, ok := blip.attr(nsR, "embed"); ok && rID != "" {
			rIDs = append(rIDs, rID)
		} else if rID, ok := blip.attr(nsR, "link"); ok && rID != "" {
			rIDs = append(rIDs, rID)
		}
	}
	for _, data := range element.descendants(nsV, "imagedata") {
		if rID, ok := data.attr(nsR, "id"); ok && rID != "" {
			rIDs = append(rIDs, rID)
		}
	}
	markers := make([]string, 0, len(rIDs))
	for _, rID := range rIDs {
		markers = append(markers, c.extractImage(rID))
	}
	return markers
}

func (c *converter) extractImage(rID string) string {
	rel, ok := c.pkg.rels[rID]
	// An external image has no part to save; python-docx's related_parts would
	// blow up on it, so treating it as broken is our own (safer) reading.
	if !ok || rel.external {
		return "(img BROKEN_IMAGE)"
	}
	name := partName(rel.target)
	data, ok := c.pkg.parts[name]
	if !ok {
		return "(img BROKEN_IMAGE)"
	}
	ext := imageExtension(c.pkg.contentType(name), name)
	c.imgCounters[ext]++
	imgname := fmt.Sprintf("%s%03d.%s", c.opts.ImagePrefix, c.imgCounters[ext], ext)
	c.images = append(c.images, Image{Name: imgname, Data: data})
	return "(img " + imgname + ")"
}

// imageExtension is _image_extension: the content type decides, and only if it
// is unknown do we fall back to the part name's own extension.
func imageExtension(contentType, name string) string {
	if ext, ok := imageExtensions[contentType]; ok {
		return ext
	}
	if ext := strings.TrimPrefix(path.Ext(name), "."); ext != "" {
		return ext
	}
	return "bin"
}

// ── list numbering ──────────────────────────────────────────────────────────

func (c *converter) listPrefix(p *node) string {
	numID, ilvl, ok := c.paragraphNumbering(p)
	if !ok {
		c.breakListIfNeeded()
		return ""
	}
	key := [2]string{numID, ilvl}
	if _, seen := c.listCounters[key]; !seen {
		c.listCounters[key] = c.num.start(numID, ilvl, false)
	} else {
		c.listCounters[key]++
	}
	return c.num.prefix(numID, ilvl, c.listCounters[key])
}

// breakListIfNeeded: with preserve_ol_start off, any non-list paragraph (blank
// ones included) restarts every list counter.
func (c *converter) breakListIfNeeded() { clear(c.listCounters) }

func (c *converter) paragraphNumbering(p *node) (numID, ilvl string, ok bool) {
	st := c.paragraphStyle(p)
	directNum, directLvl := numPrIDs(p.findPath("pPr", "numPr"))
	var styleNum, styleLvl string
	if st != nil {
		styleNum, styleLvl = numPrIDs(st.element.findPath("pPr", "numPr"))
	}
	numID = or(directNum, styleNum)
	ilvl = or(or(directLvl, styleLvl), "0")
	if numID != "" {
		if c.num.isOrdered(numID, ilvl) {
			return numID, ilvl, true
		}
		return "", "", false // a bullet list gets no prefix at all
	}
	// No w:numPr anywhere: fall back to the style-name heuristic, which catches
	// the "ListNumber"/"Номер списка" styles Word writes without a numPr.
	styleName, styleID := "", ""
	if st != nil {
		styleName, styleID = strings.ToLower(st.name), strings.ToLower(st.id)
	}
	if (strings.Contains(styleName, "number") || strings.Contains(styleID, "number") ||
		strings.Contains(styleName, "номер")) &&
		!strings.Contains(styleName, "bullet") && !strings.Contains(styleID, "bullet") {
		return "style:" + or(styleID, styleName), "0", true
	}
	return "", "", false
}

func (c *converter) paragraphStyle(p *node) *style {
	id := ""
	if ps := p.findPath("pPr", "pStyle"); ps != nil {
		id, _ = ps.wattr("val")
	}
	return c.styles.get(id)
}

func numPrIDs(numPr *node) (numID, ilvl string) {
	if numPr == nil {
		return "", ""
	}
	if n := numPr.find("numId"); n != nil {
		numID, _ = n.wattr("val")
	}
	if n := numPr.find("ilvl"); n != nil {
		ilvl, _ = n.wattr("val")
	}
	return numID, ilvl
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ── tables ──────────────────────────────────────────────────────────────────

func (c *converter) tableText(tbl *node) string {
	var rows [][]string
	for _, tr := range tbl.findAll("tr") {
		var rowData []string
		for _, tc := range rowCells(tbl, tr) {
			rowData = append(rowData, strings.Join(strings.FieldsFunc(c.cellText(tc), unicode.IsSpace), " "))
		}
		if len(rowData) > 0 {
			rows = append(rows, rowData)
		}
	}
	return markdownTable(rows)
}

func (c *converter) cellText(tc *node) string {
	var chunks []string
	for _, block := range iterBlocks(tc) {
		chunks = append(chunks, c.blockText(block))
	}
	return strings.Join(chunks, "\n")
}

// rowCells is python-docx's _Row.cells: a horizontally merged w:tc yields one
// entry per grid column it spans, and a vertically merged continuation cell
// re-yields the cell above it (so its text — and its images — repeat).
func rowCells(tbl, tr *node) []*node {
	trs := tbl.findAll("tr")
	rowIdx := 0
	for i, r := range trs {
		if r == tr {
			rowIdx = i
			break
		}
	}
	var cells []*node
	var iterTc func(rowIdx int, tc *node, depth int)
	iterTc = func(rowIdx int, tc *node, depth int) {
		if vMerge(tc) == "continue" && rowIdx > 0 && depth < len(trs) {
			if above := tcAtGridOffset(trs[rowIdx-1], gridOffset(trs[rowIdx], tc)); above != nil {
				iterTc(rowIdx-1, above, depth+1)
			}
			return
		}
		for i := 0; i < gridSpan(tc); i++ {
			cells = append(cells, tc)
		}
	}
	for _, tc := range tr.findAll("tc") {
		iterTc(rowIdx, tc, 0)
	}
	return cells
}

func gridSpan(tc *node) int {
	if gs := tc.findPath("tcPr", "gridSpan"); gs != nil {
		if v := attrInt(gs, "val", 1); v > 0 {
			return v
		}
	}
	return 1
}

// vMerge returns "" when the cell is not vertically merged; a w:vMerge with no
// w:val means "continue" (ST_Merge's default).
func vMerge(tc *node) string {
	vm := tc.findPath("tcPr", "vMerge")
	if vm == nil {
		return ""
	}
	if v, ok := vm.wattr("val"); ok {
		return v
	}
	return "continue"
}

func gridBefore(tr *node) int {
	if gb := tr.findPath("trPr", "gridBefore"); gb != nil {
		return attrInt(gb, "val", 0)
	}
	return 0
}

func gridOffset(tr, tc *node) int {
	offset := gridBefore(tr)
	for _, other := range tr.findAll("tc") {
		if other == tc {
			break
		}
		offset += gridSpan(other)
	}
	return offset
}

func tcAtGridOffset(tr *node, offset int) *node {
	remaining := offset - gridBefore(tr)
	for _, tc := range tr.findAll("tc") {
		if remaining < 0 {
			return nil
		}
		if remaining == 0 {
			return tc
		}
		remaining -= gridSpan(tc)
	}
	return nil
}

func markdownTable(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}
	for i, row := range rows {
		for len(row) < maxCols {
			row = append(row, "")
		}
		rows[i] = row
	}
	widths := make([]int, maxCols)
	for col := 0; col < maxCols; col++ {
		w := 0
		for _, row := range rows {
			if n := utf8.RuneCountInString(row[col]); n > w {
				w = n
			}
		}
		widths[col] = max(w+2, 3)
	}
	line := func(row []string) string {
		var b strings.Builder
		b.WriteString("|")
		for i, cell := range row {
			if i > 0 {
				b.WriteString("|")
			}
			b.WriteString(centerCell(cell, widths[i]))
		}
		b.WriteString("|")
		return b.String()
	}
	lines := []string{line(rows[0])}
	var sep strings.Builder
	sep.WriteString("|")
	for i, w := range widths {
		if i > 0 {
			sep.WriteString("|")
		}
		sep.WriteString(strings.Repeat("-", w))
	}
	sep.WriteString("|")
	lines = append(lines, sep.String())
	for _, row := range rows[1:] {
		lines = append(lines, line(row))
	}
	return strings.Join(lines, "\n")
}

// centerCell pads by code points, not bytes — the cells are mostly Cyrillic.
func centerCell(text string, width int) string {
	text = strings.TrimFunc(text, unicode.IsSpace)
	padding := width - utf8.RuneCountInString(text)
	left := padding / 2
	if left < 0 {
		left = 0
	}
	right := padding - left
	if right < 0 {
		right = 0
	}
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", right)
}

// ── the docx_to_text tail ───────────────────────────────────────────────────

// normalizeDocxSpacing collapses runs of spaces inside a line (Word loves to pad
// with them), leaving markdown table rows alone. Ported from parser.py; RE2 has
// no lookaround, so (?<=\S) {2,}(?=\S) is done by hand.
func normalizeDocxSpacing(text string) string {
	var out strings.Builder
	for _, line := range splitLines(text) {
		body := strings.TrimRight(line, "\r\n")
		newline := line[len(body):]
		if strings.HasPrefix(strings.TrimLeftFunc(body, unicode.IsSpace), "|") {
			out.WriteString(line)
			continue
		}
		body = strings.ReplaceAll(body, "\t", " ")
		body = collapseInnerSpaces(body)
		body = strings.TrimRight(body, " ")
		out.WriteString(body)
		out.WriteString(newline)
	}
	return out.String()
}

func collapseInnerSpaces(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); {
		if rs[i] != ' ' {
			b.WriteRune(rs[i])
			i++
			continue
		}
		j := i
		for j < len(rs) && rs[j] == ' ' {
			j++
		}
		run := j - i
		// The lookarounds want a non-whitespace character on both sides.
		if run >= 2 && i > 0 && !unicode.IsSpace(rs[i-1]) && j < len(rs) && !unicode.IsSpace(rs[j]) {
			b.WriteRune(' ')
		} else {
			b.WriteString(strings.Repeat(" ", run))
		}
		i = j
	}
	return b.String()
}

// splitLines is str.splitlines(keepends=True) — which, unlike Go, also breaks on
// the vertical tab, form feed, NEL and the Unicode line/paragraph separators.
func splitLines(s string) []string {
	var out []string
	start := 0
	rs := []byte(s)
	for i := 0; i < len(s); {
		r, sz := utf8.DecodeRune(rs[i:])
		brk := 0
		switch r {
		case '\r':
			brk = sz
			if i+sz < len(s) && s[i+sz] == '\n' {
				brk = sz + 1
			}
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			brk = sz
		}
		if brk > 0 {
			out = append(out, s[start:i+brk])
			i += brk
			start = i
			continue
		}
		i += sz
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// unquote is urllib.parse.unquote: percent-escapes decode as UTF-8, and anything
// that is not valid UTF-8 becomes U+FFFD (errors="replace").
func unquote(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '%' || i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
			b.WriteByte(s[i])
			i++
			continue
		}
		var raw []byte
		for i+2 < len(s) && s[i] == '%' && isHex(s[i+1]) && isHex(s[i+2]) {
			raw = append(raw, unhex(s[i+1])<<4|unhex(s[i+2]))
			i += 3
		}
		for len(raw) > 0 {
			r, sz := utf8.DecodeRune(raw)
			b.WriteRune(r)
			raw = raw[sz:]
		}
	}
	return b.String()
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}
