package qyserver

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"

	"qy/qy/domain/contest"
	"qy/qy/domain/question"
	"qy/qy/platform/telegram"
	"qy/qy/storage/store"
	"qy/qy/web/route"
	"qy/qy/web/ui"

	qystrings "qy/i18nstrings"
)

// The nomination form as data: what one slot holds, read from the database
// or from a post, and the page that shows it. forms.ts switches a slot's
// views; the names here are what it looks for.

const (
	kindPublished   = "published"
	kindUnpublished = "unpublished"
	viewText        = "text"
	viewFields      = "fields"
)

// maxHandoutFile caps one attached file; a whole form may carry one per slot.
const (
	maxHandoutFile    = 20 << 20
	maxNominationForm = 100 << 20
	formMemory        = 8 << 20
)

type upload struct {
	name, contentType string
	data              []byte
}

// slotInput is one slot as the nominator gave it.
type slotInput struct {
	published  bool
	link       string
	tournament string
	raw        string
	fields     question.Question // Text without the handout
	handout    string
	file       *upload
	dropFile   bool
	fileTooBig bool
}

// needsFields says whether the question is typed in rather than read from
// gotquestions by its link.
func (in slotInput) needsFields() bool {
	return !in.published || question.FindLinks(in.link).GotQuestionsID == 0
}

// loadable says the form can load the question by its link: from
// gotquestions or from a telegram post.
func (in slotInput) loadable() bool {
	return in.published && (question.FindLinks(in.link).GotQuestionsID != 0 || telegram.IsPost(in.link))
}

// fieldsGiven says the slot carries fields: typed in, or loaded from
// gotquestions and perhaps corrected.
func (in slotInput) fieldsGiven() bool {
	return in.fields != question.Question{} || in.handout != ""
}

func (in slotInput) question() question.Question {
	q := in.fields
	q.Text = question.WithHandout(q.Text, in.handout)
	return q
}

func (in slotInput) empty() bool {
	if in.published && in.link != "" {
		return false
	}
	return in.tournament == "" && in.raw == "" && in.fields == question.Question{} && in.handout == "" && in.file == nil
}

func (in slotInput) sameAs(old slotInput) bool {
	if in.published != old.published || in.link != old.link {
		return false
	}
	if !in.needsFields() && !in.fieldsGiven() && !old.fieldsGiven() {
		return true
	}
	return in.tournament == old.tournament && in.raw == old.raw && in.question() == old.question()
}

// slotView is a slot as the form shows it.
type slotView struct {
	in         slotInput
	questionID int64
	files      []store.File
	answer     string // what gotquestions gave, for a published link
	errs       []string
	parseMiss  bool
	loadMiss   bool
	view       string
}

type nominationForm struct {
	rating    string
	ratingErr error
	slots     map[int]slotView
	parseOnly bool // a parse or a load was asked for, not a save
	loadSlot  int
	uploaded  bool
}

func (f nominationForm) hasErrors() bool {
	for _, v := range f.slots {
		if len(v.errs) > 0 {
			return true
		}
	}
	return false
}

func (s *server) nominationSlots(ctx context.Context, year int, userID int64) (map[int]slotView, error) {
	rows, err := s.db.QueryContext(ctx, `
select n.slot, q.id, q.published, q.gq_id is not null, q.link, q.tournament_link, q.source_text, q.orig
from nominations n join questions q on q.id = n.question_id
where n.year = ? and n.user_id = ?`, year, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]slotView{}
	for rows.Next() {
		slot, v, err := scanSlot(rows)
		if err != nil {
			return nil, err
		}
		out[slot] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, s.attachFiles(ctx, out)
}

func scanSlot(rows *sql.Rows) (int, slotView, error) {
	var slot int
	var v slotView
	var isGQ bool
	var orig string
	if err := rows.Scan(&slot, &v.questionID, &v.in.published, &isGQ, &v.in.link, &v.in.tournament, &v.in.raw, &orig); err != nil {
		return 0, v, err
	}
	q := question.FromJSON(orig)
	if isGQ {
		v.answer = q.Answer
	}
	v.in.fields = q
	v.in.handout, v.in.fields.Text = question.SplitHandout(q.Text)
	return slot, v, nil
}

func (s *server) attachFiles(ctx context.Context, slots map[int]slotView) error {
	for slot, v := range slots {
		files, err := store.Files(ctx, s.db, v.questionID)
		if err != nil {
			return err
		}
		v.files = files
		slots[slot] = v
	}
	return nil
}

func parseNominationForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxNominationForm)
	if err := r.ParseMultipartForm(formMemory); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		return corei18n.User(qystrings.Default.Server.Error.BadRequest())
	}
	return parseForm(w, r)
}

func slotKey(slot int, name string) string { return "slot" + strconv.Itoa(slot) + "_" + name }

func formText(r *http.Request, key string) string {
	return strings.TrimSpace(strings.ReplaceAll(r.FormValue(key), "\r\n", "\n"))
}

// readNominationForm reads every slot of a post and checks it. A slot whose
// parse button was pressed, or whose fields are empty while its text is not,
// has the text parsed into its fields. A load asked for is left to the
// handler, which can reach gotquestions.
func readNominationForm(r *http.Request, c contest.Contest, before map[int]slotView) nominationForm {
	form := nominationForm{rating: r.FormValue("rating_id"), slots: map[int]slotView{}}
	parseSlot, _ := strconv.Atoi(r.FormValue("parse"))
	form.loadSlot, _ = strconv.Atoi(r.FormValue("load"))
	form.parseOnly = parseSlot != 0 || form.loadSlot != 0
	for slot := 1; slot <= c.MainSlots+c.ReserveSlots; slot++ {
		v := slotView{in: readSlot(r, slot), view: r.FormValue(slotKey(slot, "view"))}
		old := before[slot]
		v.questionID, v.files, v.answer = old.questionID, old.files, old.answer
		if v.in.file != nil {
			form.uploaded = true
		}
		if slot == parseSlot || v.in.needsFields() && v.in.raw != "" && v.in.fields == (question.Question{}) {
			v.parse()
		}
		if !form.parseOnly {
			v.errs = v.in.check()
		}
		if v.in.fileTooBig {
			v.errs = append(v.errs, str.Nominate.Error.FileTooBig())
		}
		form.slots[slot] = v
	}
	return form
}

func (v *slotView) parse() {
	q, ok := question.Parse(v.in.raw)
	v.view = viewFields
	if !ok {
		v.parseMiss = true
		return
	}
	v.in.handout, q.Text = question.SplitHandout(q.Text)
	v.in.fields = q
}

func readSlot(r *http.Request, slot int) slotInput {
	in := slotInput{
		published:  r.FormValue(slotKey(slot, "kind")) != kindUnpublished,
		link:       formText(r, slotKey(slot, "link")),
		tournament: formText(r, slotKey(slot, "tournament")),
		raw:        formText(r, slotKey(slot, "raw")),
		handout:    formText(r, slotKey(slot, "handout")),
		dropFile:   r.FormValue(slotKey(slot, "drop_file")) != "",
	}
	for _, f := range question.Fields {
		*in.fields.Ptr(f) = formText(r, slotKey(slot, f))
	}
	in.file, in.fileTooBig = readUpload(r, slotKey(slot, "file"))
	return in
}

// readUpload reads a slot's file, if one was chosen; tooBig says one was
// chosen and refused.
func readUpload(r *http.Request, key string) (_ *upload, tooBig bool) {
	f, hdr, err := r.FormFile(key)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if hdr.Size > maxHandoutFile {
		return nil, true
	}
	data, err := io.ReadAll(f)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	ct := hdr.Header.Get("Content-Type")
	if ct == "" {
		ct = http.DetectContentType(data)
	}
	return &upload{name: hdr.Filename, contentType: ct, data: data}, false
}

// requiredFields are the fields a typed-in question cannot do without.
var requiredFields = []string{question.FieldText, question.FieldAnswer, question.FieldSource, question.FieldAuthor}

// check lists what is wrong with a filled slot.
func (in slotInput) check() []string {
	if in.empty() {
		return nil
	}
	errs := in.linkErrors()
	if !in.needsFields() && !in.fieldsGiven() {
		return errs
	}
	var missing []string
	for _, f := range requiredFields {
		if in.fields.Get(f) == "" {
			missing = append(missing, nominationFieldName(f))
		}
	}
	if len(missing) > 0 {
		errs = append(errs, str.Nominate.Error.Missing(strings.Join(missing, ", ")))
	}
	return errs
}

// linkErrors checks the publication's link and the tournament's: a published
// question needs the first, an unpublished one the second.
func (in slotInput) linkErrors() []string {
	var errs []string
	switch {
	case in.published && in.link == "":
		errs = append(errs, str.Nominate.Error.NoLink())
	case in.published && !isWebLink(in.link):
		errs = append(errs, str.Nominate.Error.BadLink())
	}
	switch {
	case !in.published && in.tournament == "":
		errs = append(errs, str.Nominate.Error.NoTournament())
	case in.tournament != "" && !isWebLink(in.tournament):
		errs = append(errs, str.Nominate.Error.BadTournament())
	}
	return errs
}

func isWebLink(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// nominationFieldNames are the fields as the form labels them, which is not
// how the admin's review page does.
var nominationFieldNames = map[string]func() string{
	question.FieldText: str.Nominate.Field.Text, question.FieldAnswer: str.Nominate.Field.Answer,
	question.FieldZachet: str.Nominate.Field.Zachet, question.FieldNezachet: str.Nominate.Field.Nezachet,
	question.FieldComment: str.Nominate.Field.Comment, question.FieldSource: str.Nominate.Field.Source,
	question.FieldAuthor: str.Nominate.Field.Author,
}

func nominationFieldName(f string) string { return nominationFieldNames[f]() }

// serveFile sends a handout file. Only kinds a browser shows without running
// anything are shown inline; the rest download.
func serveFile(w http.ResponseWriter, f store.File, data []byte) {
	ct, disposition := f.ContentType, "inline"
	if !inlineSafe(ct) {
		ct, disposition = "application/octet-stream", "attachment"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": f.Name}))
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(data)
}

func inlineSafe(ct string) bool {
	base, _, _ := mime.ParseMediaType(ct)
	if base == "image/svg+xml" {
		return false
	}
	for _, p := range []string{"image/", "audio/", "video/"} {
		if strings.HasPrefix(base, p) {
			return true
		}
	}
	return base == "application/pdf" || base == "text/plain"
}

// ---- the page ----

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
	form := []ui.Item{ui.Method("post"), ui.Action("/nominate"), ui.Enctype("multipart/form-data"), ui.DirCol, ui.SpaceLG,
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

func kindRadio(slot int, kind, label string, on bool) ui.Item {
	items := []ui.Item{ui.Name(slotKey(slot, "kind")), ui.Value(kind), text(label)}
	if on {
		items = append(items, ui.Checked())
	}
	return ui.Radio(items...)
}

func slotCard(c contest.Contest, slot int, v slotView) ui.Item {
	in := v.in
	items := []ui.Item{ui.DirCol, ui.SpaceMD, ui.Data("slot", strconv.Itoa(slot)),
		ui.Strong(text(slotLabel(c, slot))),
		ui.Row(ui.SpaceMD, ui.Wrap(),
			kindRadio(slot, kindPublished, str.Nominate.Slot.Published(), in.published),
			kindRadio(slot, kindUnpublished, str.Nominate.Slot.Unpublished(), !in.published)),
		ui.Col(ui.SpaceSM, ui.Data("part", kindPublished), ui.Field(ui.Label(str.Nominate.Slot.Link()),
			linkRow(slot, v), hint(str.Nominate.Slot.LinkHint()))),
		fieldsBlock(slot, v),
	}
	items = append(items, slotStatus(v)...)
	return ui.Card(items...)
}

// linkRow is the publication's link and, for a link the form can read, the
// button that loads the question into the fields.
func linkRow(slot int, v slotView) ui.Item {
	load := []ui.Item{ui.Submit(), ui.Formnovalidate(), ui.Name("load"), ui.Value(strconv.Itoa(slot)),
		ui.IconCloudDownload, ui.Data("load", ""), text(str.Nominate.Slot.Load())}
	if !v.in.loadable() {
		load = append(load, ui.Hidden())
	}
	row := []ui.Item{ui.Row(ui.SpaceSM,
		ui.Textfield(ui.Name(slotKey(slot, "link")), ui.Value(v.in.link), ui.Inputmode("url"), ui.Grow()),
		ui.Button(load...))}
	if v.loadMiss {
		row = append(row, dangerHint(str.Nominate.Slot.LoadFailed()))
	}
	return ui.Col(append([]ui.Item{ui.SpaceSM}, row...)...)
}

// fieldsBlock is the part of a slot for a question typed in: its tournament,
// then the pasted text and the fields it was parsed into, as two views.
func fieldsBlock(slot int, v slotView) ui.Item {
	in := v.in
	view := v.view
	if view != viewText && view != viewFields {
		view = viewText
		if in.fields != (question.Question{}) {
			view = viewFields
		}
	}
	return ui.Col(ui.SpaceSM, ui.Data("part", "fields"), ui.Data("view", view),
		ui.Hiddenfield(ui.Name(slotKey(slot, "view")), ui.Value(view)),
		ui.Field(ui.Label(str.Nominate.Slot.Tournament()),
			ui.Textfield(ui.Name(slotKey(slot, "tournament")), ui.Value(in.tournament), ui.Inputmode("url"))),
		ui.Tabs(ui.Tab(ui.View(viewText), text(str.Nominate.Slot.TabText())), ui.Tab(ui.View(viewFields), text(str.Nominate.Slot.TabFields()))),
		textPanel(slot, v),
		ui.Tabpanel(append([]ui.Item{ui.Data("panel", viewFields)}, fieldEditors(slot, v)...)...),
	)
}

// textPanel is the pasted question and the button that parses it.
func textPanel(slot int, v slotView) ui.Item {
	items := []ui.Item{ui.Data("panel", viewText), ui.Col(ui.SpaceSM,
		ui.Field(ui.Label(str.Nominate.Slot.Paste()), ui.Editor(ui.Name(slotKey(slot, "raw")), ui.Rows("10"), ui.Text(v.in.raw)),
			hint(str.Nominate.Slot.PasteHint())),
		ui.Row(ui.Button(ui.Submit(), ui.Formnovalidate(), ui.Name("parse"), ui.Value(strconv.Itoa(slot)), ui.IconWandSparkles,
			text(str.Nominate.Slot.Parse()))))}
	if v.parseMiss {
		items = append(items, dangerHint(str.Nominate.Slot.ParseFailed()))
	}
	return ui.Tabpanel(items...)
}

func fieldEditors(slot int, v slotView) []ui.Item {
	in := v.in
	editor := func(f, value, rows string) ui.Item {
		label := nominationFieldName(f)
		for _, req := range requiredFields {
			if req == f {
				label += " *"
			}
		}
		return ui.Field(ui.Label(label), ui.Editor(ui.Name(slotKey(slot, f)), ui.Rows(rows), ui.Text(value)))
	}
	out := []ui.Item{ui.Col(ui.SpaceSM,
		editor(question.FieldText, in.fields.Text, "6"),
		ui.Field(ui.Label(str.Nominate.Field.Handout()), ui.Editor(ui.Name(slotKey(slot, "handout")), ui.Rows("2"), ui.Text(in.handout)),
			hint(str.Nominate.Field.HandoutHint())),
		editor(question.FieldAnswer, in.fields.Answer, "2"),
		editor(question.FieldZachet, in.fields.Zachet, "2"),
		editor(question.FieldNezachet, in.fields.Nezachet, "2"),
		editor(question.FieldComment, in.fields.Comment, "4"),
		editor(question.FieldSource, in.fields.Source, "3"),
		editor(question.FieldAuthor, in.fields.Author, "2"),
		fileField(slot, v),
	)}
	return out
}

func fileField(slot int, v slotView) ui.Item {
	items := []ui.Item{ui.Label(str.Nominate.Slot.File())}
	for _, f := range v.files {
		items = append(items, ui.Row(ui.Button(ui.Href("/nominate/file/"+idstr.Format(f.ID)), ui.Ghost, ui.Small(), ui.IconPaperclip, text(f.Name))))
	}
	if len(v.files) > 0 {
		items = append(items, checkbox(slotKey(slot, "drop_file"), str.Nominate.Slot.DropFile(), v.in.dropFile))
	}
	items = append(items, ui.Filefield(ui.Name(slotKey(slot, "file"))), hint(str.Nominate.Slot.FileHint()))
	return ui.Field(items...)
}

// slotStatus is what the form says under a slot: what was found for a
// published link, or what is wrong.
func slotStatus(v slotView) []ui.Item {
	var out []ui.Item
	for _, e := range v.errs {
		out = append(out, dangerHint(e))
	}
	if len(out) > 0 || v.questionID == 0 || v.in.needsFields() {
		return out
	}
	if v.answer != "" {
		return append(out, hint(str.Nominate.Slot.Parsed(strings.TrimRight(v.answer, "."))))
	}
	return append(out, hint(str.Nominate.Slot.LinkOnly()))
}
