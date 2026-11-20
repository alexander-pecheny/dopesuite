package qyserver

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"qy/qy/domain/lang"
	"qy/qy/domain/question"
	"qy/qy/domain/translate"
	"qy/qy/storage/store"
	"qy/qy/web/route"
	"qy/qy/web/ui"
)

// A question not in Russian has a translation of its jury's copy, which the
// background pass makes (reviewpass.go) and an admin corrects here. Only the
// admins edit it; the jury switches between it and the original.

func langName(l string) string {
	switch l {
	case lang.Russian:
		return str.Admin.Translation.Ru()
	case lang.Ukrainian:
		return str.Admin.Translation.Uk()
	case lang.Belarusian:
		return str.Admin.Translation.Be()
	}
	return str.Admin.Translation.LangUnknown()
}

// translationSection is the question page's language and translation.
func translationSection(href, keep string, q store.Question) ui.Item {
	langs := []ui.Item{ui.Name("lang")}
	if q.Lang == "" {
		langs = append(langs, option("", langName(""), ""))
	}
	for _, l := range lang.All {
		langs = append(langs, option(l, langName(l), q.Lang))
	}
	items := []ui.Item{subhead(str.Admin.Translation.Title()), hint(str.Admin.Translation.Hint()),
		ui.Form(ui.Method("post"), ui.Action(href+"/lang"+keep), ui.Row(ui.SpaceSM, ui.AlignEnd,
			ui.Field(ui.Label(str.Admin.Translation.Lang()), ui.Selectfield(langs...)),
			ui.Button(ui.Submit(), ui.Small(), text(str.Common.Save()))))}
	if !q.Foreign() {
		return ui.Section(items...)
	}
	switch {
	case q.TranslationError != "":
		items = append(items, dangerHint(str.Admin.Translation.Failed(q.TranslationError)))
	case q.TranslationStale && q.TranslationEdited:
		items = append(items, dangerHint(str.Admin.Translation.Stale()))
		if d := cleanChanges(question.FromJSON(q.TranslationOf), q.Clean); d != "" {
			items = append(items, ui.Details(ui.Summary(text(str.Admin.Translation.Changes())), ui.Richtext(ui.Raw(d))))
		}
	case q.TranslationStale && !q.Translation.Empty():
		items = append(items, hint(str.Admin.Translation.Refreshing()))
	case q.Translation.Empty():
		items = append(items, hint(str.Admin.Translation.Pending()))
	case q.TranslationEdited:
		items = append(items, hint(str.Admin.Translation.Edited()))
	default:
		items = append(items, hint(str.Admin.Translation.Machine()))
	}
	if !q.Translation.Empty() {
		form := []ui.Item{ui.Method("post"), ui.Action(href + "/translation" + keep), ui.DirCol, ui.SpaceSM}
		for _, f := range translate.Fields {
			form = append(form, ui.Field(ui.Label(fieldName(f)), ui.Editor(ui.Name(f), ui.Rows(fieldRows(f)), ui.Text(q.Translation.Get(f)))))
		}
		items = append(items, ui.Form(append(form, ui.Row(ui.Button(ui.Submit(), ui.Primary, text(str.Admin.Translation.Save()))))...))
	}
	retranslate := []ui.Item{ui.Method("post"), ui.Action(href + "/retranslate" + keep)}
	if q.TranslationEdited {
		retranslate = append(retranslate, ui.Data("confirm", str.Admin.Translation.RetranslateConfirm()))
	}
	items = append(items, ui.Form(append(retranslate,
		ui.Button(ui.Submit(), ui.Ghost, ui.IconRefreshCw, text(str.Admin.Translation.Retranslate())))...))
	return ui.Section(items...)
}

// cleanChanges is what changed in the jury's copy between before and after,
// field by field, as HTML.
func cleanChanges(before, after question.Question) string {
	var b strings.Builder
	for _, f := range question.Fields {
		if before.Get(f) == after.Get(f) {
			continue
		}
		b.WriteString("<p><b>" + plain(fieldName(f)) + "</b></p>")
		b.WriteString(diffHTML(lineDiff(before.Get(f), after.Get(f))))
	}
	return b.String()
}

func (s *server) handleSetLang(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	q, _, err := s.pathQuestion(r)
	if err != nil {
		return err
	}
	l := r.FormValue("lang")
	if !lang.Valid(l) {
		return userError(str.Server.Error.BadRequest())
	}
	if err := s.withWriteTx(r.Context(), "set-lang", func(ctx context.Context, tx *sql.Tx) error {
		return store.SetLang(ctx, tx, q.ID, l, s.stamp())
	}); err != nil {
		return err
	}
	s.nudge()
	return seeOther(w, r, questionBack(r, q.ID, doneSaved))
}

func (s *server) handleSaveTranslation(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	q, _, err := s.pathQuestion(r)
	if err != nil {
		return err
	}
	tr := q.Translation
	for _, f := range translate.Fields {
		*tr.Ptr(f) = strings.TrimSpace(strings.ReplaceAll(r.FormValue(f), "\r\n", "\n"))
	}
	if tr.Empty() {
		return userError(str.Admin.Question.OrigEmpty())
	}
	if err := s.withWriteTx(r.Context(), "save-translation", func(ctx context.Context, tx *sql.Tx) error {
		return store.SaveTranslation(ctx, tx, q.ID, tr, s.stamp())
	}); err != nil {
		return err
	}
	return seeOther(w, r, questionBack(r, q.ID, doneSaved))
}

func (s *server) handleRetranslate(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	q, _, err := s.pathQuestion(r)
	if err != nil {
		return err
	}
	if err := s.withWriteTx(r.Context(), "retranslate", func(ctx context.Context, tx *sql.Tx) error {
		return store.Retranslate(ctx, tx, q.ID, s.stamp())
	}); err != nil {
		return err
	}
	s.nudge()
	return seeOther(w, r, questionBack(r, q.ID, ""))
}
