// Package handout is a Go port of chgksuite's handout renderer
// (chgksuite/handouter), specifically the hndt2pdf path: it parses a ".hndt"
// source into blocks, emits a Typst (".typ") document using chgksuite's exact
// layout template, and (see render.go) compiles it to PDF with the typst binary.
//
// The layout itself is done entirely by Typst via the embedded template
// (assets/header.typ, taken verbatim from chgksuite); this package only
// reproduces the Python that parses .hndt and computes the per-question
// #handout(...) arguments, so the generated .typ is byte-identical to
// chgksuite's (and therefore the PDF is too — typst is deterministic).
package handout

import (
	_ "embed"
	"fmt"
	"math"
	"strconv"
	"strings"

	"xy/internal/chgk/i18n"
)

//go:embed assets/header.typ
var headerTemplate string

const (
	greytextTmpl  = "#qlabel[<GREYTEXT>]"
	celllabelTmpl = ", label: [<CELLLABEL>]"
	qgapTmpl      = "#qgap()"
	imgTmpl       = `image("<IMGPATH>", width: <IMGWIDTH>)`

	defaultFont     = "Noto Sans"
	defaultFontSize = 14
	defaultTikzMM   = 2.0 // DEFAULT_TIKZ_MM (int 2 in Python)
	space           = 1.5 // SPACE (mm, between teams)
	labelAbove      = 2.0 // LABEL_ABOVE
	labelBelow      = 0.9 // LABEL_BELOW
	strutEM         = 1.2 // STRUT_EM

	// chgksuite's defaults: A4 paper with 5 mm margins.
	a4WidthMM       = 210
	a4HeightMM      = 297
	defaultMarginMM = 5

	defaultBottomSpaceRowRatio = 0.6
	defaultShrinkPercent       = 2
	defaultMinResizeImage      = 0.6
	defaultRefineIterations    = 8

	// defaultHandoutsPerTeam is handouts_per_team when a block does not set it.
	defaultHandoutsPerTeam = 3

	mmPerInch = 25.4
	ptPerInch = 72
	percent   = 100

	// round3Scale is 10^3, for rounding to three decimals.
	round3Scale = 1000
	floatBits   = 64
)

// Args mirrors the chgksuite handout CLI flags xy relies on (ru defaults).
type Args struct {
	PaperWidth   int
	PaperHeight  int
	MarginTop    int
	MarginBottom int
	MarginLeft   int
	MarginRight  int
	Font         string   // "" → defaultFont
	FontSize     int      // 0 → 14, unless the preamble or the handout sets one
	BoxWidth     *float64 // nil → computed
	TikzMM       *float64 // nil → defaultTikzMM (int 2)
	// Resize governs split_fit's image-shrink pass; it is read there only.
	Resize ResizeConfig
	// Language is --language: which labels_*.toml the two printed captions come
	// from. Empty is Russian.
	Language string
	// LabelsFile is --labels_file: a labels TOML of one's own, in place of the
	// language's.
	LabelsFile string
}

// ResizeConfig is split_fit's image-shrink pass: when a fitted image handout
// leaves more blank space at the bottom of the page than BottomSpaceRowRatio
// row heights, the image is shrunk in ShrinkPercent steps until another row
// fits, then grown back as far as that row count allows.
type ResizeConfig struct {
	BottomSpaceRowRatio float64
	ShrinkPercent       float64
	MinResizeImage      float64
	RefineIterations    int
}

// DefaultArgs returns the chgksuite handout defaults.
func DefaultArgs() Args {
	return Args{
		PaperWidth: a4WidthMM, PaperHeight: a4HeightMM,
		MarginTop: defaultMarginMM, MarginBottom: defaultMarginMM,
		MarginLeft: defaultMarginMM, MarginRight: defaultMarginMM,
		Resize: ResizeConfig{
			BottomSpaceRowRatio: defaultBottomSpaceRowRatio,
			ShrinkPercent:       defaultShrinkPercent,
			MinResizeImage:      defaultMinResizeImage,
			RefineIterations:    defaultRefineIterations,
		},
	}
}

// pynum formats a number the way Python's str() would: ints have no decimal
// point, floats keep one (66.0 → "66.0", 2 → "2", 1.5 → "1.5").
type pynum struct {
	f     float64
	isInt bool
}

func (n pynum) String() string {
	if n.isInt {
		return strconv.Itoa(int(n.f))
	}
	return pyFloat(n.f)
}

func pyFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, floatBits)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// round3 mirrors Python round(x, 3) (round half to even).
func round3(x float64) float64 { return math.RoundToEven(x*round3Scale) / round3Scale }

// ── .hndt parsing (utils.parse_handouts) ──

var reservedWords = map[string]bool{
	"image": true, "for_question": true, "columns": true, "rows": true,
	"resize_image": true, "font_size": true, "font_family": true, "no_center": true,
	"raw_tex": true, "color": true, "handouts_per_team": true, "grouping": true,
	"rotate": true, "tikz_mm": true, "hspace": true, "vspace": true, "max_width": true,
	"question_label": true,
}

var intKeys = map[string]bool{"columns": true, "rows": true, "no_center": true, "color": true, "handouts_per_team": true}
var floatKeys = map[string]bool{"resize_image": true, "font_size": true, "tikz_mm": true, "hspace": true, "vspace": true, "max_width": true}

// block is a parsed .hndt block; values are int, float64 or string.
type block map[string]any

func (b block) str(k string) (string, bool) {
	v, ok := b[k]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
func (b block) intVal(k string) (int, bool) {
	v, ok := b[k]
	if !ok {
		return 0, false
	}
	i, ok := v.(int)
	return i, ok
}
func (b block) floatVal(k string) (float64, bool) {
	v, ok := b[k]
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	return f, ok
}

// splitBlocks splits the source on lines equal to "---" (utils.split_blocks).
func splitBlocks(contents string) []string {
	lines := strings.Split(contents, "\n")
	var groups [][]string
	cur := []string{}
	for _, ln := range lines {
		if ln == "---" {
			groups = append(groups, cur)
			cur = []string{}
		} else {
			cur = append(cur, ln)
		}
	}
	groups = append(groups, cur)
	out := make([]string, len(groups))
	for i, g := range groups {
		out[i] = strings.Join(g, "\n")
	}
	if len(out) > 0 && strings.TrimSpace(out[0]) == "" {
		out = out[1:]
	}
	return out
}

func wrapVal(key, val string) any {
	if intKeys[key] {
		n, _ := strconv.Atoi(strings.TrimSpace(val))
		return n
	}
	if floatKeys[key] {
		f, _ := strconv.ParseFloat(strings.TrimSpace(val), floatBits)
		return f
	}
	return strings.TrimSpace(val)
}

// Blocks parses a .hndt document into its blocks — the reserved keys typed
// (ints, floats, strings) and the free text under "text" — for callers that
// need to see what the generator wrote (the TS/Go parity corpus).
func Blocks(contents string) []map[string]any {
	blocks := parseHandouts(contents)
	out := make([]map[string]any, len(blocks))
	for i, b := range blocks {
		out[i] = b
	}
	return out
}

func parseHandouts(contents string) []block {
	var result []block
	for _, raw := range splitBlocks(contents) {
		b := block{}
		var text []string
		// A blank line inside the text stays: it is an empty line on the
		// handout. Only the ones around it, and the one under the settings, go.
		for _, line := range strings.Split(raw, "\n") {
			sp := strings.SplitN(line, ":", 2)
			if len(sp) == 2 && reservedWords[sp[0]] {
				b[sp[0]] = wrapVal(sp[0], sp[1])
			} else {
				text = append(text, strings.TrimSpace(line))
			}
		}
		if t := strings.TrimSpace(strings.Join(text, "\n")); t != "" {
			if _, raw := b["raw_tex"]; !raw {
				t = escapeTypst(t)
			}
			b["text"] = t
		}
		result = append(result, b)
	}
	return result
}

// escapeTypst mirrors installer.escape_typst.
func escapeTypst(text string) string {
	text = strings.ReplaceAll(text, "\\", "\\\\")
	for _, ch := range []string{"#", "$", "[", "]", "*", "_", "`", "<", ">", "@", "~"} {
		text = strings.ReplaceAll(text, ch, "\\"+ch)
	}
	text = strings.ReplaceAll(text, "//", "\\/\\/")
	text = strings.ReplaceAll(text, "/*", "\\/*")
	text = strings.ReplaceAll(text, "*/", "*\\/")
	text = strings.ReplaceAll(text, "\n", "\\\n")
	return text
}

// ── generation (HandoutGenerator) ──

func (a Args) font() string {
	if a.Font != "" {
		return a.Font
	}
	return defaultFont
}

func (a Args) fontSize() int {
	if a.FontSize == 0 {
		return defaultFontSize
	}
	return a.FontSize
}

func (a Args) header() string {
	r := strings.NewReplacer(
		"<PAPERWIDTH>", strconv.Itoa(a.PaperWidth),
		"<PAPERHEIGHT>", strconv.Itoa(a.PaperHeight),
		"<MARGIN_LEFT>", strconv.Itoa(a.MarginLeft),
		"<MARGIN_RIGHT>", strconv.Itoa(a.MarginRight),
		"<MARGIN_TOP>", strconv.Itoa(a.MarginTop),
		"<MARGIN_BOTTOM>", strconv.Itoa(a.MarginBottom),
		"<FONT>", a.font(),
		"<FONTSIZE>", strconv.Itoa(a.fontSize()),
		"<LABEL_ABOVE>", pyFloat(labelAbove),
		"<LABEL_BELOW>", pyFloat(labelBelow),
	)
	return r.Replace(headerTemplate)
}

func (a Args) pageWidth() float64 {
	return float64(a.PaperWidth - a.MarginLeft - a.MarginRight - 2)
}

func (a Args) effectiveTikzMM(b block) pynum {
	if a.TikzMM != nil {
		return pynum{*a.TikzMM, false}
	}
	if f, ok := b.floatVal("tikz_mm"); ok {
		return pynum{f, false}
	}
	return pynum{defaultTikzMM, true} // DEFAULT_TIKZ_MM is the int 2
}

func blockMaxWidth(b block) float64 {
	if f, ok := b.floatVal("max_width"); ok {
		if f > 0 && f <= 1 {
			return f
		}
	}
	return 1.0
}

// getCutDirection ports HandoutGenerator.get_cut_direction.
func getCutDirection(columns, numRows, handoutsPerTeam int, grouping string) (int, int, bool) {
	total := columns * numRows
	if handoutsPerTeam == 0 || total%handoutsPerTeam != 0 {
		return 0, 0, false
	}
	if total/handoutsPerTeam < 1 {
		return 0, 0, false
	}
	type layout struct{ cols, rows int }
	var valid []layout
	for teamRows := 1; teamRows <= handoutsPerTeam; teamRows++ {
		if handoutsPerTeam%teamRows == 0 {
			teamCols := handoutsPerTeam / teamRows
			if columns%teamCols == 0 && numRows%teamRows == 0 {
				valid = append(valid, layout{teamCols, teamRows})
			}
		}
	}
	if len(valid) == 0 {
		return 0, 0, false
	}
	// stable sort preferring smaller team_rows (horizontal) or team_cols (vertical)
	best := valid[0]
	for _, l := range valid[1:] {
		if grouping == "vertical" {
			if l.cols < best.cols {
				best = l
			}
		} else if l.rows < best.rows {
			best = l
		}
	}
	return best.cols, best.rows, true
}

func (a Args) generateForQuestion(num string) string {
	// handout_for_question carries Python's "{}" where the number goes.
	text := strings.Replace(a.labels().Text("handout_for_question"), "{}", num, 1)
	return strings.Replace(greytextTmpl, "<GREYTEXT>", text, 1)
}

func (a Args) labels() i18n.Labels { return i18n.LabelsForOrDefault(a.Language, a.LabelsFile) }

func (a Args) buildCellBody(b block) string {
	fs := pynum{float64(a.fontSize()), true}
	if f, ok := b.floatVal("font_size"); ok {
		fs = pynum{f, false}
	}
	wrapText := func(s string) string {
		if fam, ok := b.str("font_family"); ok && fam != "" {
			return fmt.Sprintf("text(font: %q, size: %spt)[%s]", fam, fs, s)
		}
		return fmt.Sprintf("text(size: %spt)[%s]", fs, s)
	}

	var imgExpr string
	if img, ok := b.str("image"); ok && img != "" {
		// Note: unlike chgksuite we don't recompress/rotate the image (typst embeds
		// it fine); the image is referenced by name from the scratch dir the caller
		// populated. This means the .typ differs from chgksuite for image handouts,
		// but the rendered PDF is equivalent.
		qw := 1.0
		if r, ok := b.floatVal("resize_image"); ok && r != 0 {
			qw = r
		}
		imgWidth := pyFloat(qw*percent) + "%"
		path := strings.ReplaceAll(img, "\\", "/")
		imgExpr = strings.ReplaceAll(strings.ReplaceAll(imgTmpl, "<IMGPATH>", path), "<IMGWIDTH>", imgWidth)
	}

	var textExpr string
	if t, ok := b.str("text"); ok && t != "" {
		textExpr = wrapText(t)
	}

	var body string
	switch {
	case imgExpr != "" && textExpr != "":
		body = fmt.Sprintf("stack(dir: ttb, spacing: 1mm, %s, align(center, %s))", imgExpr, textExpr)
	case imgExpr != "":
		body = imgExpr
	case textExpr != "":
		body = textExpr
	default:
		body = wrapText("")
	}
	return body
}

// insideLabel is the grey "Question N" that `question_label: inside` prints inside
// every cell, so a handout still names its question once it is cut out.
func (a Args) insideLabel(b block) string {
	if v, _ := b.str("question_label"); v != "inside" {
		return ""
	}
	num, ok := b.str("for_question")
	if !ok || num == "" {
		return ""
	}
	return a.labels().Field("question") + " " + num
}

func (a Args) generateRegularBlock(b block) string {
	if _, hasText := b.str("text"); !hasText {
		if _, hasImg := b["image"]; !hasImg {
			return ""
		}
	}
	columns, _ := b.intVal("columns")
	numRows := 1
	if r, ok := b.intVal("rows"); ok && r != 0 {
		numRows = r
	}
	handoutsPerTeam := defaultHandoutsPerTeam
	if h, ok := b.intVal("handouts_per_team"); ok && h != 0 {
		handoutsPerTeam = h
	}
	grouping := "horizontal"
	if g, ok := b.str("grouping"); ok && g != "" {
		grouping = g
	}

	teamCols, teamRows, teamed := getCutDirection(columns, numRows, handoutsPerTeam, grouping)
	if !teamed {
		teamCols, teamRows = columns, numRows
	}

	gap := pynum{space, false}
	if h, ok := b.floatVal("hspace"); ok && h != 0 {
		gap = pynum{h, false}
	}
	nTeamCols := columns / teamCols
	availableWidth := a.pageWidth() * blockMaxWidth(b)
	var cellw pynum
	if a.BoxWidth != nil {
		cellw = pynum{*a.BoxWidth, false}
	} else {
		cellw = pynum{round3((availableWidth - float64(nTeamCols-1)*gap.f) / float64(columns)), false}
	}
	pad := a.effectiveTikzMM(b)
	fsf := float64(a.fontSize())
	if f, ok := b.floatVal("font_size"); ok {
		fsf = f
	}
	strut := pynum{round3(fsf * strutEM * mmPerInch / ptPerInch), false}
	cellbody := a.buildCellBody(b)
	centered := "true"
	if nc, ok := b.intVal("no_center"); ok && nc != 0 {
		centered = "false"
	}
	label := a.insideLabel(b)
	if label != "" {
		label = strings.Replace(celllabelTmpl, "<CELLLABEL>", label, 1)
	}
	return fmt.Sprintf("#handout(%d, %d, %d, %d, %smm, %smm, %smm, %smm, %s, %s, %s%s)",
		columns, numRows, teamCols, teamRows, gap, cellw, pad, strut,
		strconv.FormatBool(teamed), centered, cellbody, label)
}

// GenerateTyp parses a .hndt source and returns the full .typ document, matching
// chgksuite's HandoutGenerator.generate output byte-for-byte.
func GenerateTyp(hndt string, a Args) string {
	blocks := []string{a.header()}
	for _, b := range parseHandouts(hndt) {
		if len(b) == 0 {
			blocks = append(blocks, "\n#pagebreak()\n")
			continue
		}
		var label, grid string
		if fq, ok := b.str("for_question"); ok && fq != "" {
			if ql, _ := b.str("question_label"); ql == "inside" {
				label = qgapTmpl
			} else {
				label = a.generateForQuestion(fq)
			}
		}
		if c, ok := b.intVal("columns"); ok && c != 0 {
			grid = a.generateRegularBlock(b)
		}
		if label != "" || grid != "" {
			parts := []string{}
			for _, p := range []string{label, grid} {
				if p != "" {
					parts = append(parts, p)
				}
			}
			blocks = append(blocks, strings.Join(parts, "\n\n"))
		}
	}
	return strings.Join(blocks, "\n\n")
}
