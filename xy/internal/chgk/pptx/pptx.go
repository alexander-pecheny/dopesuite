// Package pptx is composer/pptx.py: a package as the slide deck it is played
// from. One slide per question, a plug slide to hold on while the teams write,
// and an answer slide; a handout or a picture gets a slide of its own first.
//
// The presentation is built on chgksuite's own template.pptx and written the way
// python-pptx writes one, so the two can be compared slide XML for slide XML.
// The one part that cannot be identical is the shrink-to-fit measurement — see
// measure.go for why.
package pptx

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/i18n"
	"xy/internal/chgk/inline"
	"xy/internal/chgk/typo"
)

// Options are the switches `compose pptx` takes that are not in the config file.
type Options struct {
	// Config is pptx_config.toml; nil is the one chgksuite ships.
	Config *Config
	// Template is the .pptx to build on; nil is chgksuite's own.
	Template []byte
	// FontDirs are extra places to look for the measurement font.
	FontDirs []string
	// DisableNumbers is --disable_numbers: no question number in the corner.
	DisableNumbers bool
	// DoNotRemoveAccents is --do_not_remove_accents: stress marks stay.
	DoNotRemoveAccents bool
	// Language is --language; only "ru" changes anything, by tagging runs.
	Language string
	// NoBreak is --replace_no_break_spaces / --replace_no_break_hyphens.
	NoBreak inline.NoBreak
	// OptimizeSize is --optimize_size: re-encode the pictures once the deck is
	// built. On by default in chgksuite, and the difference between a 30 MB
	// presentation and a few megabytes.
	OptimizeSize bool
}

// Export renders a package. images maps a picture's name, as the (img …)
// directives spell it, to its bytes.
func Export(doc fsource.Doc, images map[string][]byte, o Options) ([]byte, error) {
	cfg := o.Config
	if cfg == nil {
		var err error
		if cfg, err = DefaultConfig(); err != nil {
			return nil, err
		}
	}
	template := o.Template
	if template == nil {
		template = defaultTemplate
	}
	p, err := openPkg(template)
	if err != nil {
		return nil, err
	}
	e := &exporter{
		pkg: p, cfg: cfg, opts: o, images: images, labels: i18n.LabelsOrDefault(o.Language),
		faces: FindFontFaces(cfg.fontName(), o.FontDirs),
	}
	if err := e.resolveLayouts(); err != nil {
		return nil, err
	}
	if err := e.run(doc); err != nil {
		return nil, err
	}
	if o.OptimizeSize {
		p.optimizeImages(80)
	}
	return p.save()
}

type exporter struct {
	pkg    *pkg
	cfg    *Config
	opts   Options
	images map[string][]byte
	faces  *fontFaces
	labels i18n.Labels

	titleLayout, blankLayout    string
	questionLayout              string
	answerLayout, plugLayout    string
	qcount                      int
	number                      string
	processedQuestions          int
	processedTours              int
	pendingImages               []*pendingImage
	usedTemplateSlide           bool
	serviceTemplates            map[string][]*slidePart
	serviceIndicesToRemove      []int
	templateSlideCount          int
	handoutRe1, handoutRe2      *regexp.Regexp
	slideOfPendingImagesCounter int
}

// resolveLayouts is the head of export(): which of the template's layouts each
// kind of slide is built on.
func (e *exporter) resolveLayouts() error {
	pick := func(key string, fallback int) (string, error) {
		return e.pkg.layoutTarget(e.cfg.layoutIndex(key, fallback))
	}
	var err error
	if e.titleLayout, err = pick("title_slide_index", 0); err != nil {
		return err
	}
	if e.blankLayout, err = pick("blank_slide_index", 6); err != nil {
		return err
	}
	e.questionLayout, e.answerLayout, e.plugLayout = e.blankLayout, e.blankLayout, e.blankLayout
	if e.cfg.templateVersion() >= 2 {
		if e.questionLayout, err = pick("question_slide_index", 1); err != nil {
			return err
		}
		if e.answerLayout, err = pick("answer_slide_index", 2); err != nil {
			return err
		}
		if e.plugLayout, err = pick("plug_slide_index", 3); err != nil {
			return err
		}
	}
	return nil
}

// run is the body of export(): questions one at a time, everything between them
// buffered until the next one arrives.
func (e *exporter) run(doc fsource.Doc) error {
	label := e.labels.Field("handout")
	e.handoutRe1 = regexp.MustCompile(`(?s)\[` + regexp.QuoteMeta(label) + `.(.+?)\]`)
	e.handoutRe2 = regexp.MustCompile(`^` + regexp.QuoteMeta(label) + `.(.+?)$`)
	e.templateSlideCount = len(e.pkg.slides)

	if err := e.prepareServiceSlides(); err != nil {
		return err
	}
	e.addServiceSlides("intro")

	var buffer fsource.Doc
	for _, el := range doc {
		if el.Type != "Question" {
			buffer = append(buffer, el)
			continue
		}
		if len(buffer) > 0 {
			if e.shouldAddBetweenTours(buffer) {
				e.addServiceSlides("between_tours")
			}
			if err := e.processBuffer(buffer); err != nil {
				return err
			}
			buffer = nil
		}
		q, ok := el.Content.(*fsource.Question)
		if !ok {
			continue
		}
		if err := e.processQuestion(q); err != nil {
			return err
		}
		e.processedQuestions++
	}
	e.addServiceSlides("final")
	e.removeServiceSlideTemplates()
	return nil
}

// ── text ────────────────────────────────────────────────────────────────────

var (
	reSpaces      = regexp.MustCompile(` +`)
	reHandoutTail = regexp.MustCompile(`(?s)\[[Рр][Аа][Зз][Дд][Аа][Тт].+?: ?(.+)\]`)
	reHandoutCut  = regexp.MustCompile(`(?s)\[[Рр][Аа][Зз][Дд][Аа][Тт](.+?)\]`)
	reHandoutWord = regexp.MustCompile(`[Рр][Аа][Зз][Дд][Аа][Тт]`)
	reSizedImage  = regexp.MustCompile(`(?:^|\s)[wh]=`)
)

// processText is pptx_process_text: what a field's text looks like on a slide.
func (e *exporter) processText(v any, hasImage, stripBrackets, replaceSpaces, keepAccents bool) any {
	if list, ok := v.([]any); ok {
		out := make([]any, len(list))
		for i, x := range list {
			out[i] = e.processText(x, hasImage, stripBrackets, replaceSpaces, keepAccents)
		}
		return out
	}
	s, ok := v.(string)
	if !ok {
		return v
	}
	if !e.opts.DoNotRemoveAccents && !keepAccents {
		s = inline.RemoveAccents(s)
	}
	if stripBrackets {
		s = inline.RemoveSquareBrackets(s)
		s = strings.ReplaceAll(s, "]\n", "]\n\n")
	} else {
		s = inline.ReplaceEscaped(s)
	}
	switch {
	case hasImage:
		s = strings.TrimSpace(reHandoutCut.ReplaceAllString(s, ""))
	case reHandoutWord.MatchString(s) && !e.cfg.includeHandoutLabel():
		if m := reHandoutTail.FindStringSubmatch(s); m != nil {
			s = strings.Replace(s, m[0], m[1], 1)
		}
	}
	s = reSpaces.ReplaceAllString(s, " ")
	for _, punct := range []string{".", ",", "!", "?", ":"} {
		s = strings.ReplaceAll(s, " "+punct, punct)
	}
	if replaceSpaces {
		s = inline.ReplaceNoBreak(s, e.opts.NoBreak)
	}
	return strings.TrimSpace(s)
}

// text is pptx_process_text with the defaults every caller but two uses.
func (e *exporter) text(v any) any { return e.processText(v, false, true, true, false) }

// configureParagraph is _configure_paragraph: the font the whole paragraph is
// set in, before any run overrides it.
func (e *exporter) configureParagraph(p *paragraph, size float64, align, lineSpacingKey string) *paragraph {
	p.fontName = e.cfg.fontName()
	if size == 0 {
		size = e.cfg.fontSize("default_size", 32)
	}
	p.size = size
	e.setLineSpacing(p, size, lineSpacingKey)
	if align != "" {
		p.align = pptxAlign(align)
	}
	return p
}

func pptxAlign(s string) string {
	switch strings.ToLower(s) {
	case "left":
		return "l"
	case "right":
		return "r"
	case "center":
		return "ctr"
	case "justify":
		return "just"
	}
	return ""
}

// setLineSpacing is _set_line_spacing: an exact height for this kind of
// paragraph, a multiplier for all of them, or the font size itself.
func (e *exporter) setLineSpacing(p *paragraph, size float64, key string) {
	font := e.cfg.fontTable()
	if key != "" {
		if v, ok := tableNum(font, "fixed_line_spacing_"+key); ok {
			p.lineSpacePt = v
			return
		}
	}
	if v, ok := tableNum(font, "line_spacing_multiplier"); ok {
		p.lineSpacing = v
		return
	}
	if tableBool(font, "fixed_line_spacing", false) {
		p.lineSpacePt = size
	}
}

// addRuns is add_runs: one run per line of the text, with <a:br/> between them.
// A text of nothing still makes one run, which is what holds the paragraph open.
func (e *exporter) addRuns(p *paragraph, text string, color string) []*run {
	var made []*run
	// set_pptx_run_text: a no-break hyphen becomes a plain one fenced by word
	// joiners, because LibreOffice draws U+2011 as a missing glyph but honours
	// U+2060 on either side of an ordinary hyphen.
	text = strings.ReplaceAll(text, "‑", "\u2060-\u2060")
	for i, part := range strings.Split(text, "\n") {
		if i > 0 {
			p.runs = append(p.runs, &run{br: true})
		}
		if part == "" {
			continue
		}
		r := &run{text: part}
		e.applyRunDefaults(r, p, color)
		p.runs = append(p.runs, r)
		made = append(made, r)
	}
	if len(made) == 0 {
		r := &run{}
		e.applyRunDefaults(r, p, color)
		p.runs = append(p.runs, r)
		made = append(made, r)
	}
	return made
}

func (e *exporter) addRun(p *paragraph, text string) *run {
	runs := e.addRuns(p, text, "")
	return runs[len(runs)-1]
}

func (e *exporter) applyRunDefaults(r *run, p *paragraph, color string) {
	if p.fontName != "" {
		r.fontName = p.fontName
	}
	if p.size > 0 {
		r.size, r.sizeSet = p.size, true
	}
	if color == "" {
		color = colorOf(e.cfg.textbox(), "color")
	}
	if color != "" {
		r.color = color
	}
	if e.opts.Language == "ru" {
		r.language, r.langSet = "ru-RU", true
	}
}

func applyStyle(runs []*run, style string) {
	for _, r := range runs {
		if strings.Contains(style, "italic") {
			r.italic, r.italicSet = true, true
		}
		if strings.Contains(style, "bold") {
			r.bold, r.boldSet = true, true
		}
		if strings.Contains(style, "underline") {
			r.underline, r.underlineSet = true, true
		}
	}
}

// hyperlinkColor is the blue chgksuite paints a link, and the underline goes
// with it.
const hyperlinkColor = "0563C1"

func (e *exporter) addHyperlinkRuns(s *slidePart, p *paragraph, text, url string) {
	if !e.cfg.formatLinks() {
		e.addRuns(p, text, "")
		return
	}
	quoted := inline.URLQuote(url)
	relID := s.addRel(relHyperlink, quoted, true)
	for _, r := range e.addRuns(p, text, "") {
		r.hyperlink, r.hyperlinkRelID = quoted, relID
		r.underline, r.underlineSet = true, true
		r.color = hyperlinkColor
	}
}

// format is pptx_format: a value laid into a paragraph, run by run. A list
// becomes numbered items; a nested list is a preamble and its sub-questions.
func (e *exporter) format(v any, p *paragraph, t *textbox, s *slidePart, replaceSpaces, blankLinesBetween bool) {
	if list, ok := v.([]any); ok {
		if len(list) > 1 {
			if inner, nested := list[1].([]any); nested {
				e.format(list[0], p, t, s, replaceSpaces, blankLinesBetween)
				e.formatItems(inner, p, t, s, replaceSpaces, blankLinesBetween, true)
				return
			}
		}
		e.formatItems(list, p, t, s, replaceSpaces, blankLinesBetween, false)
		return
	}
	str, ok := v.(string)
	if !ok {
		return
	}
	e.formatString(str, p, t, s, replaceSpaces)
}

func (e *exporter) formatItems(items []any, p *paragraph, t *textbox, s *slidePart, replaceSpaces, blankLinesBetween, sub bool) {
	blankLine := e.cfg.blankLineBeforeItems()
	for i, li := range items {
		n := i + 1
		prefix := "\n"
		if sub {
			if blankLine && (n == 1 || blankLinesBetween) {
				prefix = "\n\n"
			}
		} else if blankLine && blankLinesBetween && n > 1 {
			prefix = "\n\n"
		}
		e.addRun(p, prefix+e.cfg.listMarker(n)+" ")
		e.format(li, p, t, s, replaceSpaces, blankLinesBetween)
	}
}

func (e *exporter) formatString(str string, p *paragraph, t *textbox, s *slidePart, replaceSpaces bool) {
	sp := func(text string) string {
		if replaceSpaces {
			return inline.ReplaceNoBreak(text, e.opts.NoBreak)
		}
		return text
	}
	for _, r := range inline.Parse4sElem(inline.BacktickReplace(str)) {
		switch r.Kind {
		case "screen":
			e.addRuns(p, sp(r.ForScreen), "")
		case "linebreak":
			e.addRun(p, "\n")
		case "hyperlink":
			e.addHyperlinkRuns(s, p, r.Text, r.Text)
		case "img":
			if img, ok := e.parseImage(r.Text); ok && img.inline {
				e.addInlineImage(p, s, img, r.Text)
			}
		case "strike":
			runs := e.addRuns(p, sp(r.Text), "")
			for _, run := range runs {
				run.strike, run.strikeSet = true, true
			}
		default:
			applyStyle(e.addRuns(p, sp(r.Text), ""), r.Kind)
		}
	}
}

// plainLinesForMeasurement is _plain_text_lines_for_measurement: what a handout
// reads as, with the markup taken out, so its size can be measured line by line.
func (e *exporter) plainLinesForMeasurement(text string) []string {
	lines := []string{""}
	appendText := func(v string) {
		parts := strings.Split(v, "\n")
		lines[len(lines)-1] += parts[0]
		lines = append(lines, parts[1:]...)
	}
	for _, r := range inline.Parse4sElem(inline.BacktickReplace(text)) {
		switch r.Kind {
		case "screen":
			appendText(r.ForScreen)
		case "linebreak":
			lines = append(lines, "")
		case "img", "pagebreak":
		default:
			appendText(r.Text)
		}
	}
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// ── odds and ends ───────────────────────────────────────────────────────────

// label is get_label: a question's own !!Label override, and the plural
// «Источники» when it names more than one.
func (e *exporter) label(q *fsource.Question, field string) string {
	if ov, ok := q.Get("overrides").(map[string]string); ok {
		if v, ok := ov[field]; ok && v != "" {
			return v
		}
	}
	if field == "source" {
		if _, isList := q.Get("source").([]any); isList {
			return e.labels.Field("sources")
		}
	}
	if v := e.labels.Field(field); v != "" {
		return v
	}
	return field
}

func recursiveJoin(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			parts[i] = recursiveJoin(x)
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// textForGrid is _text_for_grid: a value as one string, for measuring how long
// it is.
func textForGrid(v any) string {
	switch t := v.(type) {
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			parts[i] = textForGrid(x)
		}
		return strings.Join(parts, "\n")
	case string:
		return t
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

func tryInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		return n, err == nil
	}
	return 0, false
}

var _ = typo.HasURL
