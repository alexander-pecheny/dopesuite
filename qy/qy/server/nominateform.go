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

	"qy/qy/domain/contest"
	"qy/qy/domain/question"
	"qy/qy/platform/telegram"
	"qy/qy/storage/store"

	qystrings "qy/i18nstrings"
)

// The nomination form as data: what one slot holds, read from the database
// or from a post. nominateview.go shows it, and nominate.ts switches a
// slot's views; the names here are what it looks for.

const (
	viewPreview = "preview"
	viewFields  = "fields"
	viewText    = "text"
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

// slotInput is one slot as the nominator gave it. raw is the box the slot
// starts with: a link to load the question from, or the whole question to
// parse. A question is published when it has a publication link.
type slotInput struct {
	link       string
	tournament string
	appeal     string
	raw        string
	fields     question.Question // Text without the handout
	handout    string
	file       *upload
	dropFile   bool
	fileTooBig bool
}

func (in slotInput) published() bool { return in.link != "" }

// needsFields says whether the question is typed in rather than read from
// gotquestions by its link.
func (in slotInput) needsFields() bool {
	return question.FindLinks(in.link).GotQuestionsID == 0
}

// isSingleLink says the source box holds a link and nothing else.
func isSingleLink(raw string) bool {
	return raw != "" && !strings.ContainsAny(raw, " \t\n") && isWebLink(raw)
}

// loadable says the form can load the question by this link: from
// gotquestions or from a telegram post.
func loadable(link string) bool {
	return question.FindLinks(link).GotQuestionsID != 0 || telegram.IsPost(link)
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
	return in.link == "" && in.tournament == "" && in.raw == "" && in.fields == question.Question{} && in.handout == "" && in.file == nil
}

func (in slotInput) sameAs(old slotInput) bool {
	if in.link != old.link {
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
	lint       lintResult
	view       string
	from       int // the slot the question was in when the page was drawn, 0 for this one
}

// origin is the slot this one's question came from: itself unless the
// nominator moved it.
func (v slotView) origin(slot int) int {
	if v.from == 0 {
		return slot
	}
	return v.from
}

// editing says the slot shows its question rather than only the source
// box: it has fields, was saved, or failed to parse or load.
func (v slotView) editing() bool {
	return v.in.fieldsGiven() || v.questionID != 0 || v.parseMiss || v.loadMiss || v.in.link != "" || v.in.tournament != ""
}

type nominationForm struct {
	rating    string
	ratingErr error
	slots     map[int]slotView
	parseOnly bool // a parse, a load or a clear was asked for, not a save
	loads     []int
	uploaded  bool
}

// check lists the mistakes of every slot, once the loads are done.
func (f nominationForm) check() {
	for slot, v := range f.slots {
		if !f.parseOnly {
			v.errs = append(v.errs, v.in.check()...)
		}
		if v.in.fileTooBig {
			v.errs = append(v.errs, str.Nominate.Error.FileTooBig())
		}
		f.slots[slot] = v
	}
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
select n.slot, q.id, q.published, q.gq_id is not null, q.link, q.tournament_link, q.source_text, q.orig, n.appeal
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
	var published bool
	if err := rows.Scan(&slot, &v.questionID, &published, &isGQ, &v.in.link, &v.in.tournament, &v.in.raw, &orig, &v.in.appeal); err != nil {
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

// readNominationForm reads every slot of a post. A slot whose parse button
// was pressed, or whose fields are empty while its source box holds a text,
// has the text parsed into its fields. A link in the box to be loaded, asked
// for or not, is left to the handler, which can reach gotquestions.
func readNominationForm(r *http.Request, c contest.Contest, before map[int]slotView) nominationForm {
	form := nominationForm{rating: r.FormValue("rating_id"), slots: map[int]slotView{}}
	parseSlot, _ := strconv.Atoi(r.FormValue("parse"))
	loadSlot, _ := strconv.Atoi(r.FormValue("load"))
	clearSlot, _ := strconv.Atoi(r.FormValue("clear"))
	moveSlot, moveTo := readMove(r.FormValue("move"), c.MainSlots+c.ReserveSlots)
	form.parseOnly = parseSlot != 0 || loadSlot != 0 || clearSlot != 0 || moveSlot != 0
	for slot := 1; slot <= c.MainSlots+c.ReserveSlots; slot++ {
		if slot == clearSlot {
			form.slots[slot] = slotView{}
			continue
		}
		from := slot
		if n, err := strconv.Atoi(r.FormValue(slotKey(slot, "origin"))); err == nil && n >= 1 && n <= c.MainSlots+c.ReserveSlots {
			from = n
		}
		old := before[from]
		v := slotView{in: readSlot(r, slot), view: r.FormValue(slotKey(slot, "view")), from: from}
		v.questionID, v.files, v.answer = old.questionID, old.files, old.answer
		if v.in.file != nil {
			form.uploaded = true
		}
		unfilled := v.in.raw != "" && v.in.fields == (question.Question{})
		switch {
		case slot == loadSlot || unfilled && isSingleLink(v.in.raw) && slot != parseSlot:
			form.loads = append(form.loads, slot)
		case slot == parseSlot || unfilled || v.textEdited():
			v.parse()
		}
		form.slots[slot] = v
	}
	if moveSlot != 0 {
		form.slots[moveSlot], form.slots[moveTo] = form.slots[moveTo], form.slots[moveSlot]
	}
	return form
}

// textEdited says the nominator changed the question in the text tab and
// saved from there, so the text and not the fields is the question.
func (v slotView) textEdited() bool {
	return v.view == viewText && v.in.fieldsGiven() && v.in.raw != "" && !isSingleLink(v.in.raw) &&
		v.in.raw != question.Format(v.in.question())
}

// readMove reads a move button, "3:up" or "3:down", as the two slots to swap.
func readMove(raw string, slots int) (from, to int) {
	n, dir, ok := strings.Cut(raw, ":")
	slot, err := strconv.Atoi(n)
	if !ok || err != nil {
		return 0, 0
	}
	to = slot + 1
	if dir == "up" {
		to = slot - 1
	}
	if slot < 1 || slot > slots || to < 1 || to > slots {
		return 0, 0
	}
	return slot, to
}

func (v *slotView) parse() {
	q, ok := question.Parse(v.in.raw)
	v.view = viewPreview
	if !ok {
		v.view = viewText
		v.parseMiss = true
		return
	}
	v.in.handout, q.Text = question.SplitHandout(q.Text)
	v.in.fields = q
}

func readSlot(r *http.Request, slot int) slotInput {
	in := slotInput{
		link:       formText(r, slotKey(slot, "link")),
		tournament: formText(r, slotKey(slot, "tournament")),
		appeal:     formText(r, slotKey(slot, "appeal")),
		raw:        formText(r, slotKey(slot, "raw")),
		handout:    formText(r, slotKey(slot, "handout")),
		dropFile:   r.FormValue(slotKey(slot, "drop_file")) != "",
	}
	for _, f := range question.Fields {
		*in.fields.Ptr(f) = formText(r, slotKey(slot, f))
	}
	in.file, in.fileTooBig = readUpload(r, slotKey(slot, "file"))
	if in.link == "" && isSingleLink(in.raw) {
		in.link = in.raw
	}
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
	if !in.needsFields() {
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

// linkErrors checks the publication's link and the tournament's: a question
// with no publication needs its tournament.
func (in slotInput) linkErrors() []string {
	var errs []string
	if in.link != "" && !isWebLink(in.link) {
		errs = append(errs, str.Nominate.Error.BadLink())
	}
	switch {
	case !in.published() && in.tournament == "":
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
