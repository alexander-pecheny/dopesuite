package qyserver

import (
	"encoding/json"
	"net/http"

	"qy/qy/domain/question"
	"qy/qy/storage/store"
	"qy/qy/web/ui"
)

// The jury's copy on the admin's question page, under the nomination form's
// three tabs: the preview, the fields and the whole text. nominate.ts drives
// them as it drives a slot. The card's controls carry the fields' own names
// (data-prefix is empty), so the review form reads them as it always has;
// the handout has a field of its own here and goes back in front of the text
// on saving.

const handoutField = "handout"

// questionEditor opens on the tab view: the preview on the question's page,
// the fields on a portion's, where the jury's card is right above.
func questionEditor(q store.Question, view string) ui.Item {
	handout, rest := question.SplitHandout(q.Clean.Text)
	fields := q.Clean
	fields.Text, fields.Author = rest, q.Orig.Author
	whole := fields
	whole.Text = q.Clean.Text
	editor := func(name, label, value string, extra ...ui.Item) ui.Item {
		return ui.Field(append(extra, ui.Data("field", name), ui.Label(label),
			ui.Editor(ui.Name(name), ui.Rows("1"), ui.Data("autogrow", ""), ui.Text(value)))...)
	}
	optional := ui.Data("optional", "")
	adds := []ui.Item{ui.SpaceSM, ui.Wrap(), ui.Data("adds", ""), ui.Hidden()}
	for _, f := range []struct{ name, label string }{
		{handoutField, str.Nominate.Field.Handout()},
		{question.FieldZachet, fieldName(question.FieldZachet)},
		{question.FieldNezachet, fieldName(question.FieldNezachet)},
	} {
		adds = append(adds, ui.Button(ui.Ghost, ui.Small(), ui.Data("add", f.name), ui.Hidden(), text(str.Nominate.Slot.Add(f.label))))
	}
	images, _ := json.Marshal(imageLinks(q))
	return ui.Card(ui.DirCol, ui.SpaceMD, ui.Data("question-editor", ""), ui.Data("prefix", ""),
		ui.Data("state", stateEditor), ui.Data("view", view), ui.Data("images", string(images)),
		ui.Hiddenfield(ui.Name("view"), ui.Value(view)),
		ui.Tabs(ui.Data("part", "tabs"),
			ui.Tab(ui.View(viewPreview), text(str.Nominate.Slot.TabPreview())),
			ui.Tab(ui.View(viewFields), text(str.Nominate.Slot.TabFields())),
			ui.Tab(ui.View(viewText), text(str.Nominate.Slot.TabText()))),
		ui.Tabpanel(ui.Data("panel", viewPreview), ui.Col(ui.SpaceSM, ui.Richtext())),
		ui.Tabpanel(ui.Data("panel", viewFields), ui.Col(ui.SpaceSM,
			editor(question.FieldText, fieldName(question.FieldText), fields.Text),
			editor(handoutField, str.Nominate.Field.Handout(), handout, optional),
			editor(question.FieldAnswer, fieldName(question.FieldAnswer), fields.Answer),
			editor(question.FieldZachet, fieldName(question.FieldZachet), fields.Zachet, optional),
			editor(question.FieldNezachet, fieldName(question.FieldNezachet), fields.Nezachet, optional),
			editor(question.FieldComment, fieldName(question.FieldComment), fields.Comment),
			editor(question.FieldSource, fieldName(question.FieldSource), fields.Source),
			editor(question.FieldAuthor, fieldName(question.FieldAuthor), fields.Author),
			ui.Row(adds...))),
		ui.Tabpanel(ui.Data("panel", viewText), ui.Field(ui.Label(str.Nominate.Slot.TextLabel()),
			ui.Editor(ui.Name("raw"), ui.Rows("1"), ui.Data("autogrow", ""), ui.Text(question.Format(whole))))),
	)
}

// editedQuestion is the jury's copy and its authors as the form sent them:
// the whole text if the admin left the form on that tab and changed it,
// else the fields.
func editedQuestion(r *http.Request) question.Question {
	var q question.Question
	for _, f := range question.Fields {
		*q.Ptr(f) = formText(r, f)
	}
	q.Text = question.WithHandout(q.Text, formText(r, handoutField))
	raw := formText(r, "raw")
	if r.FormValue("view") != viewText || raw == "" || raw == question.Format(q) {
		return q
	}
	if parsed, ok := question.Parse(raw); ok {
		return parsed
	}
	return q
}
