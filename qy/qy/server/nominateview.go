package qyserver

import (
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"

	"qy/qy/domain/contest"
	"qy/qy/domain/question"
	"qy/qy/storage/store"
	"qy/qy/web/route"
	"qy/qy/web/ui"
)

// The nomination page. A slot starts as one box, for a link or a whole
// question. Once it is loaded or parsed, the slot shows the question under
// three tabs (the preview, the fields and the box again) and its two links.
// nominate.ts does the switching; data-state, data-part, data-panel and
// data-optional are what it looks for.

const (
	stateSource = "source"
	stateEditor = "editor"
)

func closedNominationView(c contest.Contest) view {
	return view{title: str.Nominate.Page.Title(), trail: []crumb{{"", str.Nominate.Page.Title()}},
		body: []ui.Item{ui.Section(hint(str.Nominate.Page.Closed(dayText(c.NominationStart), dayText(c.NominationEnd))))}}
}

func (s *server) nominationView(r *http.Request, c contest.Contest, sc route.Scope, f nominationForm) view {
	lead := []ui.Item{
		hint(str.Nominate.Page.Lead(strconv.Itoa(c.MainSlots), strconv.Itoa(c.ReserveSlots), dayText(c.NominationEnd))),
		hint(str.Nominate.Page.Eligible(dayText(c.QuestionsFrom), dayText(c.QuestionsTo))),
		hint(str.Nominate.Page.HowTo()),
	}
	if c.StageAt(s.now()) != contest.Nomination && sc.IsAdmin {
		lead = append(lead, hint(str.Nominate.Page.AdminPreview()))
	}
	lead = append(lead, noticeOf(r)...)
	if f.hasErrors() || f.ratingErr != nil {
		lead = append(lead, dangerHint(str.Nominate.Page.NotSaved()))
		if f.uploaded {
			lead = append(lead, dangerHint(str.Nominate.Page.FilesLost()))
		}
	}
	return view{title: str.Nominate.Page.Title(), trail: []crumb{{"", str.Nominate.Page.Title()}},
		body: []ui.Item{ui.Section(lead...), ui.Section(ui.Form(nominationFormItems(c, f)...))}}
}

func nominationFormItems(c contest.Contest, f nominationForm) []ui.Item {
	rating := []ui.Item{ui.Label(str.Common.RatingId()), ui.Textfield(ui.Name("rating_id"), ui.Value(f.rating), ui.Inputmode("numeric"), ui.Required())}
	if f.ratingErr != nil {
		_, msg := errorOf(f.ratingErr)
		rating = append(rating, dangerHint(msg))
	}
	form := []ui.Item{ui.Method("post"), ui.Action(nominatePath(c.Year)), ui.Enctype("multipart/form-data"), ui.DirCol, ui.SpaceLG,
		ui.Data("nominate", ""), ui.Field(rating...)}
	for slot := 1; slot <= c.MainSlots+c.ReserveSlots; slot++ {
		if slot == c.MainSlots+1 {
			form = append(form, subhead(str.Nominate.Page.Reserves()))
		}
		form = append(form, slotCard(c, slot, f.slots[slot]))
	}
	return append(form, ui.Row(ui.Button(ui.Primary, ui.Submit(), text(str.Nominate.Page.Submit()))))
}

func slotLabel(c contest.Contest, slot int) string {
	if slot <= c.MainSlots {
		return str.Nominate.Slot.Main(strconv.Itoa(slot))
	}
	return str.Nominate.Slot.Reserve(strconv.Itoa(slot - c.MainSlots))
}

// slotState and slotViewName are where a slot opens: the box alone, or the
// question on the tab it was left at.
func slotState(v slotView) (state, view string) {
	if !v.editing() {
		return stateSource, viewText
	}
	switch v.view {
	case viewPreview, viewFields, viewText:
		return stateEditor, v.view
	}
	if v.parseMiss || v.loadMiss {
		return stateEditor, viewText
	}
	return stateEditor, viewPreview
}

// moveButton swaps a slot with the one above or below it; the order of the
// main questions is the nominator's order of preference.
func moveButton(slot int, dir string, icon ui.Item, label string, hidden bool) ui.Item {
	items := []ui.Item{ui.Submit(), ui.Formnovalidate(), ui.Name("move"), ui.Value(strconv.Itoa(slot) + ":" + dir), ui.Ghost, ui.Small(),
		icon, ui.Data("move", dir), ui.Title(label), ui.Aria("label", label)}
	if hidden {
		items = append(items, ui.Hidden())
	}
	return ui.Button(items...)
}

func slotCard(c contest.Contest, slot int, v slotView) ui.Item {
	state, view := slotState(v)
	last := c.MainSlots + c.ReserveSlots
	clear := []ui.Item{ui.Submit(), ui.Formnovalidate(), ui.Name("clear"), ui.Value(strconv.Itoa(slot)), ui.Ghost, ui.Small(),
		ui.IconX, ui.Data("clear", ""), text(str.Nominate.Slot.Clear())}
	tabs := []ui.Item{ui.Data("part", "tabs"),
		ui.Tab(ui.View(viewPreview), text(str.Nominate.Slot.TabPreview())),
		ui.Tab(ui.View(viewFields), text(str.Nominate.Slot.TabFields())),
		ui.Tab(ui.View(viewText), text(str.Nominate.Slot.TabText()))}
	links := []ui.Item{ui.Data("part", "links")}
	if state == stateSource {
		clear, tabs, links = append(clear, ui.Hidden()), append(tabs, ui.Hidden()), append(links, ui.Hidden())
	}
	items := []ui.Item{ui.DirCol, ui.SpaceMD, ui.Data("slot", strconv.Itoa(slot)), ui.Data("state", state), ui.Data("view", view),
		ui.Hiddenfield(ui.Name(slotKey(slot, "view")), ui.Value(view)),
		ui.Hiddenfield(ui.Name(slotKey(slot, "origin")), ui.Value(strconv.Itoa(v.origin(slot)))),
		ui.Row(ui.Data("head", ""), ui.SpaceXS, ui.AlignCenter, ui.Strong(ui.Grow(), text(slotLabel(c, slot))),
			moveButton(slot, "up", ui.IconArrowUp, str.Nominate.Slot.MoveUp(), slot == 1),
			moveButton(slot, "down", ui.IconArrowDown, str.Nominate.Slot.MoveDown(), slot == last),
			ui.Button(clear...)),
		ui.Tabs(tabs...),
		previewPanel(v),
		ui.Tabpanel(append([]ui.Item{ui.Data("panel", viewFields)}, fieldEditors(slot, v)...)...),
		sourcePanel(slot, v),
		ui.Col(append(append(links, linkFields(slot, v)...), lintBox(c, slot, v))...),
	}
	items = append(items, slotStatus(v)...)
	return ui.Card(items...)
}

// sourcePanel is the box for a link or a whole question, and the button
// that loads the one or parses the other.
//
// Once the slot holds a question, the box holds it as text instead, the
// way Parse reads it, and nominate.ts keeps the box and the fields in step.
func sourcePanel(slot int, v slotView) ui.Item {
	editing := v.in.fieldsGiven()
	raw := v.in.raw
	if editing {
		raw = question.Format(v.in.question())
	}
	link := isSingleLink(raw)
	load := []ui.Item{ui.Submit(), ui.Formnovalidate(), ui.Name("load"), ui.Value(strconv.Itoa(slot)),
		ui.IconCloudDownload, ui.Data("load", ""), text(str.Nominate.Slot.Load())}
	parse := []ui.Item{ui.Submit(), ui.Formnovalidate(), ui.Name("parse"), ui.Value(strconv.Itoa(slot)),
		ui.IconWandSparkles, ui.Data("parse", ""), text(str.Nominate.Slot.Parse())}
	if link {
		parse = append(parse, ui.Hidden())
	} else {
		load = append(load, ui.Hidden())
	}
	label := str.Nominate.Slot.Source()
	if editing {
		label = str.Nominate.Slot.TextLabel()
	}
	items := []ui.Item{ui.Data("panel", viewText), ui.Field(ui.Label(label),
		ui.Row(ui.SpaceSM, ui.AlignStart,
			ui.Editor(ui.Name(slotKey(slot, "raw")), ui.Rows("1"), ui.Grow(), ui.Data("autogrow", ""), ui.Text(raw)),
			ui.Button(load...), ui.Button(parse...)))}
	if v.loadMiss {
		items = append(items, dangerHint(str.Nominate.Slot.LoadFailed()))
	}
	if v.parseMiss {
		items = append(items, dangerHint(str.Nominate.Slot.ParseFailed()))
	}
	return ui.Tabpanel(items...)
}

func previewPanel(v slotView) ui.Item {
	items := []ui.Item{ui.Data("panel", viewPreview), ui.Col(ui.SpaceSM,
		ui.Richtext(ui.Raw(previewHTML(v.in.fields, v.in.handout))))}
	for _, f := range v.files {
		items = append(items, fileLink(f))
	}
	return ui.Tabpanel(items...)
}

func fileLink(f store.File) ui.Item {
	return ui.Row(ui.Button(ui.Href("/nominate/file/"+idstr.Format(f.ID)), ui.Ghost, ui.Small(), ui.IconPaperclip, text(f.Name)))
}

var imageLinkRe = regexp.MustCompile(`(?i)^https?://\S+\.(?:jpe?g|png|gif|webp)(?:\?\S*)?$`)

// previewHTML is a slot's question as the preview tab shows it, laid out
// as xy's preview lays out a question (web/ts/preview.ts there): the label,
// the handout in a frame with its caption, the text, then each field on a
// line of its own, the sources and the authors smaller.
func previewHTML(q question.Question, handout string) string {
	if q.Text == "" && q.Answer == "" {
		return "<p>" + html.EscapeString(str.Nominate.Slot.PreviewEmpty()) + "</p>"
	}
	var b strings.Builder
	label := `<strong class="pv-label">` + html.EscapeString(str.Question.Unnumbered()) + " </strong>"
	b.WriteString(`<article class="pv-q">`)
	if handout != "" {
		b.WriteString(`<div class="pv-q-text">` + label + `</div>`)
		b.WriteString(`<div class="pv-handout-box u-col"><div class="pv-handout-caption">` + html.EscapeString(str.Question.Handout()) +
			`</div><div class="pv-handout-frame">` + handoutHTML(handout) + `</div></div>`)
		label = ""
	}
	b.WriteString(`<div class="pv-q-text">` + label + plain(q.Text) + `</div>`)
	for _, f := range []struct{ label, value, cls string }{
		{str.Question.Answer(), q.Answer, ""},
		{str.Question.Zachet(), q.Zachet, ""},
		{str.Question.Nezachet(), q.Nezachet, ""},
		{str.Question.Comment(), q.Comment, ""},
		{str.Question.Source(), q.Source, " pv-small"},
		{str.Question.Author(), q.Author, " pv-small"},
	} {
		if f.value != "" {
			b.WriteString(`<div class="pv-field` + f.cls + `"><strong class="pv-label">` + html.EscapeString(f.label) + " </strong>" + plain(f.value) + "</div>")
		}
	}
	b.WriteString("</article>")
	return b.String()
}

// handoutHTML is a handout's lines, a picture's link shown as the picture.
func handoutHTML(handout string) string {
	var lines []string
	for _, line := range strings.Split(handout, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		if imageLinkRe.MatchString(line) {
			lines = append(lines, `<img class="pv-img" src="`+html.EscapeString(line)+`" alt="`+html.EscapeString(line)+`" loading="lazy">`)
		} else {
			lines = append(lines, plain(line))
		}
	}
	return strings.Join(lines, "<br>")
}

// fieldEditors are the question's fields, each one line until it holds
// more. An empty optional field hides behind a «+» button under the rest.
func fieldEditors(slot int, v slotView) []ui.Item {
	in := v.in
	editor := func(f, value, rows string, extra ...ui.Item) ui.Item {
		label := nominationFieldName(f)
		for _, req := range requiredFields {
			if req == f {
				label += " *"
			}
		}
		return ui.Field(append(extra, ui.Data("field", f), ui.Label(label),
			ui.Editor(ui.Name(slotKey(slot, f)), ui.Rows(rows), ui.Data("autogrow", ""), ui.Text(value)))...)
	}
	handout := ui.Field(ui.Data("field", "handout"), ui.Data("optional", ""), ui.Label(str.Nominate.Field.Handout()),
		ui.Editor(ui.Name(slotKey(slot, "handout")), ui.Rows("1"), ui.Data("autogrow", ""), ui.Text(in.handout)),
		fileField(slot, v))
	adds := []ui.Item{ui.SpaceSM, ui.Wrap(), ui.Data("adds", ""), ui.Hidden()}
	for _, f := range []struct{ name, label string }{
		{"handout", str.Nominate.Field.Handout()},
		{question.FieldZachet, str.Nominate.Field.Zachet()},
		{question.FieldNezachet, str.Nominate.Field.Nezachet()},
	} {
		adds = append(adds, ui.Button(ui.Ghost, ui.Small(), ui.Data("add", f.name), ui.Hidden(), text(str.Nominate.Slot.Add(f.label))))
	}
	return []ui.Item{ui.Col(ui.SpaceSM,
		editor(question.FieldText, in.fields.Text, "2"),
		handout,
		editor(question.FieldAnswer, in.fields.Answer, "1"),
		editor(question.FieldZachet, in.fields.Zachet, "1", ui.Data("optional", "")),
		editor(question.FieldNezachet, in.fields.Nezachet, "1", ui.Data("optional", "")),
		editor(question.FieldComment, in.fields.Comment, "1"),
		editor(question.FieldSource, in.fields.Source, "1"),
		editor(question.FieldAuthor, in.fields.Author, "1"),
		ui.Row(adds...),
	)}
}

func fileField(slot int, v slotView) ui.Item {
	items := []ui.Item{ui.Label(str.Nominate.Slot.File())}
	for _, f := range v.files {
		items = append(items, fileLink(f))
	}
	if len(v.files) > 0 {
		items = append(items, checkbox(slotKey(slot, "drop_file"), str.Nominate.Slot.DropFile(), v.in.dropFile))
	}
	items = append(items, ui.Filefield(ui.Name(slotKey(slot, "file"))))
	return ui.Field(items...)
}

// linkFields are the publication's link and the tournament's.
func linkFields(slot int, v slotView) []ui.Item {
	return []ui.Item{ui.SpaceSM,
		ui.Field(ui.Label(str.Nominate.Slot.Link()),
			ui.Textfield(ui.Name(slotKey(slot, "link")), ui.Value(v.in.link), ui.Inputmode("url"))),
		ui.Field(ui.Label(str.Nominate.Slot.Tournament()),
			ui.Textfield(ui.Name(slotKey(slot, "tournament")), ui.Value(v.in.tournament), ui.Inputmode("url"))),
	}
}

// slotStatus is what the form says under a slot: what was found for a
// gotquestions link saved as it is, or what is wrong.
func slotStatus(v slotView) []ui.Item {
	var out []ui.Item
	for _, e := range v.errs {
		out = append(out, dangerHint(e))
	}
	if len(out) > 0 || v.questionID == 0 || v.in.needsFields() || v.in.fieldsGiven() {
		return out
	}
	if v.answer != "" {
		return append(out, hint(str.Nominate.Slot.Parsed(strings.TrimRight(v.answer, "."))))
	}
	return append(out, hint(str.Nominate.Slot.LinkOnly()))
}
