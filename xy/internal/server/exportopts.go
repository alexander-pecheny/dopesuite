package server

import (
	"archive/zip"
	"bytes"
	"regexp"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/docx"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/pptx"
	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"

	xystrings "xy/i18nstrings"
)

// The export modal's .docx and .pptx settings, which are chgksuite's compose
// switches sent as form fields beside the formats.

// fontFamily is what a font field may hold: a family name, never a path, since
// chgksuite reads a font file when the name is one.
var fontFamily = regexp.MustCompile(`^[\p{L}\p{N} -]{0,64}$`)

// formOff turns off a setting that is on by default, as chgksuite has it, so a
// client that sends no settings (xy-cli) keeps chgksuite's defaults.
const formOff = "0"

// maxTemplateInflated bounds what an uploaded template unpacks to, read off
// its directory before anything is unpacked.
const maxTemplateInflated = 64 << 20

type exportSettings struct {
	docx docx.Options
	pptx pptx.Options
}

func readExportSettings(form *memForm, game string, commentFirst bool) (exportSettings, error) {
	d, err := readDocxSettings(form, game, commentFirst)
	if err != nil {
		return exportSettings{}, err
	}
	p, err := readPptxSettings(form, commentFirst)
	if err != nil {
		return exportSettings{}, err
	}
	return exportSettings{docx: d, pptx: p}, nil
}

func readDocxSettings(form *memForm, game string, commentFirst bool) (docx.Options, error) {
	d := docx.Options{
		Game:                    game,
		CommentFirst:            commentFirst,
		ScreenMode:              docx.ScreenOff,
		Spoilers:                docx.SpoilersOff,
		NoParagraph:             form.Value("docx_noparagraph") == formOn,
		OnlyQuestionNumber:      form.Value("docx_only_question_number") == formOn,
		SameSourceAndAuthorSize: form.Value("docx_same_source_size") == formOn,
		OptimizeSize:            form.Value("docx_optimize") != formOff,
		Font:                    form.Value("docx_font"),
	}
	if !fontFamily.MatchString(d.Font) {
		return d, corei18n.User(xystrings.Default.Server.Export.BadFont())
	}
	switch v := form.Value("docx_answers"); v {
	case "whiten", "pagebreak", "dots":
		d.Spoilers = docx.Spoilers(v)
	case "none":
		d.NoAnswers = true
	}
	switch v := docx.ScreenMode(form.Value("docx_screen_mode")); v {
	case docx.ScreenReplaceAll, docx.ScreenAddVersions, docx.ScreenAddVersionsColumns:
		d.ScreenMode = v
	}
	var err error
	d.Template, err = templateFile(form, "docx_template", xystrings.Default.Server.Export.BadDocxTemplate())
	return d, err
}

func readPptxSettings(form *memForm, commentFirst bool) (pptx.Options, error) {
	p := pptx.Options{
		CommentFirst:       commentFirst,
		Font:               form.Value("pptx_font"),
		DisableNumbers:     form.Value("pptx_disable_numbers") == formOn,
		DoNotRemoveAccents: form.Value("pptx_keep_accents") == formOn,
		OptimizeSize:       form.Value("pptx_optimize") != formOff,
	}
	if !fontFamily.MatchString(p.Font) {
		return p, corei18n.User(xystrings.Default.Server.Export.BadFont())
	}
	var err error
	if p.Template, err = templateFile(form, "pptx_template", xystrings.Default.Server.Export.BadPptxTemplate()); err != nil {
		return p, err
	}
	if files := form.Files("pptx_config"); len(files) > 0 {
		if p.Config, err = pptx.ParseConfig(string(files[0].Data)); err != nil {
			return p, corei18n.User(xystrings.Default.Server.Export.BadPptxConfig(err.Error()))
		}
	}
	return p, nil
}

// templateFile is an uploaded .docx or .pptx to build on, or nil. It is
// refused when it is not a zip, or would unpack to more than the bound.
func templateFile(form *memForm, key, refusal string) ([]byte, error) {
	files := form.Files(key)
	if len(files) == 0 {
		return nil, nil
	}
	data := files[0].Data
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, corei18n.User(refusal)
	}
	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
	}
	if total > maxTemplateInflated {
		return nil, corei18n.User(refusal)
	}
	return data, nil
}

// screenCopy is o for the copy the screen shows, which the export modal offers
// as formats of their own: the screen's text, and that text with the answers
// behind dots for written testing.
func screenCopy(o docx.Options, spoilers docx.Spoilers) docx.Options {
	o.ScreenMode = docx.ScreenReplaceAll
	o.Spoilers = spoilers
	o.NoAnswers = false
	return o
}
