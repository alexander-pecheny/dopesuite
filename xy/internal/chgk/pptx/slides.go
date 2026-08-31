package pptx

import (
	"math"
	"strconv"
	"strings"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/inline"
)

// The slide builders, in the order export() reaches them: the title, the block
// between tours, the question and its handout or picture, the plug, the answer.

// textboxAt is get_textbox: the box the config puts the text in.
func (e *exporter) textboxAt(s *slidePart) *textbox {
	t := e.cfg.textbox()
	return s.addTextbox(
		inches(tableNumOr(t, "left", 0)), inches(tableNumOr(t, "top", 0)),
		inches(tableNumOr(t, "width", 0)), inches(tableNumOr(t, "height", 0)))
}

// numberTextbox is get_textbox_qnumber: the corner the question number sits in.
func (e *exporter) numberTextbox(s *slidePart) *textbox {
	n, t := e.cfg.numberTextbox(), e.cfg.textbox()
	pick := func(key string) int64 {
		if v, ok := tableNum(n, key); ok {
			return inches(v)
		}
		return inches(tableNumOr(t, key, 0))
	}
	return s.addTextbox(pick("left"), pick("top"), pick("width"), pick("height"))
}

// applyVerticalAlignment is apply_vertical_alignment_if_needed: a box told to
// centre its text loses its top and bottom insets, which is what makes the
// centring land where it should.
func (e *exporter) applyVerticalAlignment(t *textbox) {
	align := tableStr(e.cfg.textbox(), "vertical_align", "")
	if align == "" {
		return
	}
	t.tf.marginTop, t.tf.marginBottom = 0, 0
	switch strings.ToLower(align) {
	case "top":
		t.tf.verticalAnchor = "t"
	case "middle":
		t.tf.verticalAnchor = "ctr"
	case "bottom":
		t.tf.verticalAnchor = "b"
	}
}

// setQuestionNumber is set_question_number.
func (e *exporter) setQuestionNumber(s *slidePart, number string) {
	if e.opts.DisableNumbers {
		return
	}
	cfg := e.cfg.numberTextbox()
	box := e.numberTextbox(s)
	p := e.configureParagraph(box.tf.first(), 0, tableStr(cfg, "align", ""), "")
	if e.cfg.str("question_number_format", "") == "caps" {
		if _, ok := tryInt(number); ok {
			number = "ВОПРОС " + number
		}
	}
	r := e.addRun(p, number)
	if tableBool(cfg, "bold", false) {
		r.bold, r.boldSet = true, true
	}
	if c := colorOf(cfg, "color"); c != "" {
		r.color = c
	}
	size, ok := tableNum(cfg, "font_size")
	if !ok {
		size = e.cfg.fontSize("number_size", 0)
	}
	if size > 0 {
		r.size, r.sizeSet = size, true
		e.setLineSpacing(p, size, "number")
	}
}

// processBuffer is process_buffer: the title slide, then the editors and the
// tour heading.
func (e *exporter) processBuffer(buffer fsource.Doc) error {
	var headingBlock, editorBlock, sectionBlock fsource.Doc
	block := &headingBlock
	for _, el := range buffer {
		if el.Type == "section" {
			block = &sectionBlock
		}
		if el.Type == "editor" && len(sectionBlock) == 0 {
			block = &editorBlock
		}
		*block = append(*block, el)
	}
	title := firstOfType(headingBlock, "ljheading")
	if title == nil {
		title = firstOfType(headingBlock, "heading")
	}
	date := firstOfType(headingBlock, "date")
	if title != nil && !e.cfg.skipGeneratedTitle() {
		e.titleSlide(textForGrid(title.Content), date)
	}
	e.processBlock(editorBlock)
	if len(sectionBlock) > 0 {
		e.addNumberedTourStub()
		e.processBlock(sectionBlock)
		e.processedTours++
	}
	return nil
}

func firstOfType(d fsource.Doc, typ string) *fsource.Pair {
	for i := range d {
		if d[i].Type == typ {
			return &d[i]
		}
	}
	return nil
}

// titleSlide fills the template's own first slide when it is still the only one,
// and otherwise adds one on the title layout.
func (e *exporter) titleSlide(title string, date *fsource.Pair) {
	titleFrame := newTextFrame()
	titleP := titleFrame.first()
	titleP.size = e.cfg.fontSize("title_size", 60)
	e.setLineSpacing(titleP, titleP.size, "title")
	r := &run{text: title, size: titleP.size, sizeSet: true, fontName: e.cfg.headingFontName()}
	titleP.runs = append(titleP.runs, r)

	var subtitleFrame *textFrame
	if date != nil {
		subtitleFrame = newTextFrame()
		p := subtitleFrame.first()
		p.size = e.cfg.fontSize("default_size", 32)
		e.setLineSpacing(p, p.size, "default")
		p.runs = append(p.runs, &run{
			text: textForGrid(date.Content), size: p.size, sizeSet: true,
			fontName: e.cfg.headingFontName(),
		})
	}

	if !e.usedTemplateSlide && e.templateSlideCount == 1 && len(e.serviceTemplates) == 0 {
		e.usedTemplateSlide = true
		s := e.pkg.slides[0]
		s.edits = []*editedShape{{tf: titleFrame}, {tf: subtitleFrame, remove: subtitleFrame == nil}}
		if subtitleFrame == nil {
			e.spreadTitleOverSlide(s)
		}
		return
	}
	// Otherwise a slide of its own, carrying the title layout's placeholders.
	s := e.pkg.addSlide(e.titleLayout)
	placeholders := e.pkg.clonePlaceholders(s, e.titleLayout)
	if len(placeholders) > 0 {
		placeholders[0].tf = titleFrame
	}
	if len(placeholders) > 1 {
		if subtitleFrame != nil {
			placeholders[1].tf = subtitleFrame
		} else {
			placeholders[1].dropped = true
		}
	}
}

// spreadTitleOverSlide is the half of format_title_slide that runs when there is
// no subtitle: the heading takes the whole text box's place.
func (e *exporter) spreadTitleOverSlide(s *slidePart) {
	dim := func(key string, fallback float64) int64 {
		if v, ok := tableNum(e.cfg.table("title_textbox"), key); ok {
			return inches(v)
		}
		if v, ok := tableNum(e.cfg.textbox(), key); ok {
			return inches(v)
		}
		return inches(fallback)
	}
	box := [4]int64{dim("left", 1.67), dim("top", 0.8), dim("width", 8.86), dim("height", 6.1)}
	s.edits[0].xfrm = &box
	s.edits[0].tf.marginTop, s.edits[0].tf.marginBottom = 0, 0
	s.edits[0].tf.verticalAnchor = "ctr"
}

// processBlock is _process_block: the tour heading, the editors and any meta,
// all on one slide.
func (e *exporter) processBlock(block fsource.Doc) {
	section := firstOfType(block, "section")
	editor := firstOfType(block, "editor")
	var meta fsource.Doc
	for _, el := range block {
		if el.Type == "meta" {
			meta = append(meta, el)
		}
	}
	if section == nil && editor == nil && len(meta) == 0 {
		return
	}
	s := e.pkg.addSlide(e.blankLayout)
	box := e.textboxAt(s)
	e.applyVerticalAlignment(box)
	p := e.configureParagraph(box.tf.first(), 0, "", "")

	addBreak := false
	if section != nil {
		if mode := e.cfg.str("tour_as_question_number", ""); mode != "" {
			txt := textForGrid(e.text(section.Content))
			if mode == "caps" {
				txt = strings.ToUpper(txt)
			}
			e.setQuestionNumber(s, txt)
		} else {
			r := e.addRun(p, inline.ReplaceNoBreak(textForGrid(e.text(section.Content)), e.opts.NoBreak))
			if f := e.cfg.headingFontName(); f != "" {
				r.fontName = f
			}
			r.size, r.sizeSet = e.cfg.fontSize("tour_size", 32), true
			addBreak = true
		}
	}
	for _, el := range append(fsource.Doc{}, nonNil(editor)...) {
		if addBreak {
			e.addRun(p, "\n\n")
		}
		e.format(e.text(el.Content), p, box, s, true, false)
		addBreak = true
	}
	for _, el := range meta {
		if addBreak {
			e.addRun(p, "\n\n")
		}
		e.format(e.text(el.Content), p, box, s, true, false)
		addBreak = true
	}
	e.shrink(box, 0)
	e.placeInlineImages(box, s)
}

func nonNil(p *fsource.Pair) fsource.Doc {
	if p == nil {
		return nil
	}
	return fsource.Doc{*p}
}

// processQuestion is process_question: the question, the plug, the answer. A
// blitz gets one slide per sub-question, each showing one more than the last.
func (e *exporter) processQuestion(q *fsource.Question) error {
	if !q.Has("number") {
		e.qcount++
	}
	if v, ok := q.Get("setcounter").(string); ok {
		if n, err := strconv.Atoi(v); err == nil {
			e.qcount = n
		}
	}
	e.number = strconv.Itoa(e.qcount)
	if n := q.Get("number"); n != nil {
		e.number = textForGrid(n)
	}

	if list, ok := q.Get("question").([]any); ok && len(list) > 1 {
		if inner, nested := list[1].([]any); nested {
			for i := range inner {
				partial := cloneQuestion(q)
				partial.Set("question", []any{list[0], append([]any{}, inner[:i+1]...)})
				e.processQuestionText(partial)
			}
		} else {
			e.processQuestionText(q)
		}
	} else {
		e.processQuestionText(q)
	}

	if e.cfg.addPlug() {
		s := e.pkg.addSlide(e.plugLayout)
		e.setQuestionNumber(s, e.number)
	}
	e.addAnswerSlide(q)
	return nil
}

func cloneQuestion(q *fsource.Question) *fsource.Question {
	out := fsource.NewQuestion()
	for _, k := range q.Keys() {
		out.Set(k, q.Get(k))
	}
	return out
}

// processQuestionText is process_question_text: the picture or the handout on a
// slide of its own, then the question itself.
func (e *exporter) processQuestionText(q *fsource.Question) {
	image := e.imageFrom(q.Get("question"))
	handout := e.handoutFrom(q.Get("question"))
	separate := e.cfg.addHandoutOnSeparateSlide()
	switch {
	case image != nil && separate:
		e.addSlideWithImage(image, e.number)
	case handout != "" && separate:
		e.addSlideWithHandout(handout, e.number)
	}
	s := e.pkg.addSlide(e.questionLayout)
	duplicated := e.cfg.textIsDuplicated()
	e.putQuestionOnSlide(image, s, q, !duplicated)
	if image != nil && image.big && duplicated {
		e.addSlideWithImage(image, e.number)
	}
}

// putQuestionOnSlide is put_question_on_slide.
func (e *exporter) putQuestionOnSlide(image *slideImage, s *slidePart, q *fsource.Question, allowBigImage bool) {
	box := e.makeSlideLayout(image, s, allowBigImage)
	e.applyVerticalAlignment(box)
	e.setQuestionNumber(s, e.number)

	question := q.Get("question")
	handout := ""
	if image == nil {
		handout, question = e.splitHandoutFromText(question)
	}
	questionText := e.processText(question, image != nil, true, true, false)
	questionSize := e.cfg.fontSizeForText("question", textForGrid(questionText), "question_size", 32)

	var p *paragraph
	if handout != "" {
		handoutText := e.processText(handout, false, true, true, true)
		handoutP := e.configureParagraph(box.tf.first(),
			e.handoutFontSizeForText(textForGrid(handoutText), box, questionSize), "", "handout")
		if a := tableStr(e.cfg.handout(), "align", ""); a != "" {
			handoutP.align = pptxAlign(a)
		}
		e.format(handoutText, handoutP, box, s, true, false)
		handoutP.spaceAfter = e.cfg.handoutTextSpaceAfter()
		p = e.configureParagraph(box.tf.addParagraph(), questionSize, "", "question")
	} else {
		p = e.configureParagraph(box.tf.first(), questionSize, "", "question")
	}
	e.format(questionText, p, box, s, true, true)
	e.shrink(box, 0)
	e.placeInlineImages(box, s)
}

// addSlideWithHandout is add_slide_with_handout.
func (e *exporter) addSlideWithHandout(handout, number string) {
	s := e.pkg.addSlide(e.questionLayout)
	box := e.textboxAt(s)
	e.applyVerticalAlignment(box)
	if number != "" {
		e.setQuestionNumber(s, number)
	}
	handoutText := e.processText(handout, false, true, true, true)
	p := e.configureParagraph(box.tf.first(),
		e.handoutFontSizeForText(textForGrid(handoutText), box, 0), "", "handout")
	if a := tableStr(e.cfg.handout(), "align", ""); a != "" {
		p.align = pptxAlign(a)
	}
	e.format(handoutText, p, box, s, true, false)
	e.shrink(box, 0)
	e.placeInlineImages(box, s)
}

// addAnswerSlide is add_answer_slide: the answer and whatever the config says
// goes with it, each label in bold.
func (e *exporter) addAnswerSlide(q *fsource.Question) {
	s := e.pkg.addSlide(e.answerLayout)
	if caption := e.cfg.str("override_answer_caption", ""); caption != "" {
		e.setQuestionNumber(s, caption)
	} else {
		e.setQuestionNumber(s, e.number)
	}

	fields := []string{"answer"}
	if q.Get("zachet") != nil && e.cfg.addZachet() {
		fields = append(fields, "zachet")
	}
	if q.Get("nezachet") != nil && e.cfg.addZachet() {
		fields = append(fields, "nezachet")
	}
	if e.cfg.addComment() && q.Has("comment") {
		fields = append(fields, "comment")
	}
	if e.cfg.addSource() && q.Has("source") {
		fields = append(fields, "source")
	}
	if e.cfg.addAuthor() && q.Has("author") {
		fields = append(fields, "author")
	}
	answerSize := e.cfg.fontSizeForText("answer", e.answerGridText(q, fields), "answer_size", 32)

	var box *textbox
	for _, field := range fields {
		if image := e.imageFrom(q.Get(field)); image != nil {
			box = e.makeSlideLayout(image, s, true)
			break
		}
	}
	if box == nil {
		box = e.textboxAt(s)
	}
	e.applyVerticalAlignment(box)

	p := e.configureParagraph(box.tf.first(), answerSize, "", "answer")
	r := e.addRun(p, e.label(q, "answer")+": ")
	r.bold, r.boldSet = true, true
	e.format(e.processText(q.Get("answer"), false, false, true, false), p, box, s, true, false)

	for _, field := range fields[1:] {
		stripBrackets := field != "zachet"
		value := e.processText(q.Get(field), false, stripBrackets, true, false)
		r := e.addRun(p, "\n"+e.label(q, field)+": ")
		r.bold, r.boldSet = true, true
		e.format(value, p, box, s, true, false)
	}
	e.shrink(box, 0)
	e.placeInlineImages(box, s)
}

// answerGridText is _get_answer_grid_text: how long the whole answer slide's
// text is, which is what the size grid measures.
func (e *exporter) answerGridText(q *fsource.Question, fields []string) string {
	var parts []string
	for _, field := range fields {
		stripBrackets := field != "answer" && field != "zachet"
		value := e.processText(q.Get(field), false, stripBrackets, true, false)
		parts = append(parts, e.label(q, field)+": "+textForGrid(value))
	}
	return strings.Join(parts, "\n")
}

// ── service slides ──────────────────────────────────────────────────────────

// prepareServiceSlides is _prepare_service_slide_templates: the template's own
// slides that the config nominates as intros, interludes and finales, taken out
// of the deck and kept to be cloned.
func (e *exporter) prepareServiceSlides() error {
	e.serviceTemplates = map[string][]*slidePart{}
	configured := e.cfg.configuredServiceSlideIndices()
	if len(configured) == 0 {
		return nil
	}
	for _, i := range configured {
		if i < 0 || i >= len(e.pkg.slides) {
			return errIndexOutOfRange(i)
		}
	}
	for _, key := range []string{"intro", "between_tours", "final"} {
		for _, i := range e.cfg.serviceSlideIndices(key) {
			e.serviceTemplates[key] = append(e.serviceTemplates[key], e.pkg.slides[i])
		}
	}
	for _, i := range e.cfg.numberedTourStubIndices() {
		e.serviceTemplates["numbered_tours_stubs"] = append(e.serviceTemplates["numbered_tours_stubs"], e.pkg.slides[i])
	}
	seen := map[int]bool{}
	for _, i := range configured {
		if !seen[i] {
			seen[i] = true
			e.serviceIndicesToRemove = append(e.serviceIndicesToRemove, i)
		}
	}
	sortDesc(e.serviceIndicesToRemove)
	return nil
}

func (e *exporter) addServiceSlides(key string) {
	for _, src := range e.serviceTemplates[key] {
		e.cloneSlide(src)
	}
}

func (e *exporter) addNumberedTourStub() {
	slides := e.serviceTemplates["numbered_tours_stubs"]
	if e.processedTours < len(slides) {
		e.cloneSlide(slides[e.processedTours])
	}
}

// cloneSlide is _clone_slide: the same shapes on a new slide, with the pictures
// and links they refer to related to it too.
func (e *exporter) cloneSlide(src *slidePart) *slidePart {
	s := e.pkg.addSlide(src.layout)
	remap := map[string]string{}
	for _, r := range src.rels {
		if r.relType == relLayout {
			continue
		}
		remap[r.id] = s.addRel(r.relType, r.target, r.external)
	}
	raw := string(src.raw)
	s.clonedBG = slideBackground(raw)
	s.clonedShapes = remapRelIDs(spTreeShapes(raw), remap)
	return s
}

func (e *exporter) removeServiceSlideTemplates() {
	for _, i := range e.serviceIndicesToRemove {
		e.pkg.removeSlide(i)
	}
}

func (e *exporter) shouldAddBetweenTours(buffer fsource.Doc) bool {
	if e.processedQuestions == 0 {
		return false
	}
	for _, el := range buffer {
		if el.Type == "section" {
			return true
		}
	}
	return false
}

func sortDesc(v []int) {
	for i := range v {
		for j := i + 1; j < len(v); j++ {
			if v[j] > v[i] {
				v[i], v[j] = v[j], v[i]
			}
		}
	}
}

func errIndexOutOfRange(i int) error {
	return &indexError{i}
}

type indexError struct{ index int }

func (e *indexError) Error() string {
	return "service slide index " + strconv.Itoa(e.index) + " is out of range for the template"
}

// roundHalf is Python's round() on a positive number, which the placeholder
// width uses.
func roundHalf(v float64) int { return int(math.Round(v)) }
