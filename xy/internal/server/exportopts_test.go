package server

import (
	"archive/zip"
	"bytes"
	"testing"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/docx"
)

func settingsOf(t *testing.T, values map[string]string, files map[string][]memFile) (exportSettings, error) {
	t.Helper()
	return readExportSettings(&memForm{values: values, files: files}, "chgk", false)
}

func TestExportSettingsDefaultsAreChgksuites(t *testing.T) {
	s, err := settingsOf(t, map[string]string{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !s.docx.OptimizeSize || !s.pptx.OptimizeSize || s.docx.NoAnswers || s.docx.Spoilers != docx.SpoilersOff {
		t.Fatalf("defaults: %+v / %+v", s.docx, s.pptx)
	}
}

func TestExportSettingsAnswersAndScreen(t *testing.T) {
	s, err := settingsOf(t, map[string]string{"docx_answers": "none", "docx_screen_mode": "add_versions", "docx_optimize": "0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !s.docx.NoAnswers || s.docx.ScreenMode != docx.ScreenAddVersions || s.docx.OptimizeSize {
		t.Fatalf("got %+v", s.docx)
	}
	screen := screenCopy(s.docx, docx.SpoilersDots)
	if screen.NoAnswers || screen.ScreenMode != docx.ScreenReplaceAll || screen.Spoilers != docx.SpoilersDots {
		t.Fatalf("the written-testing copy must show the answers behind dots: %+v", screen)
	}
}

func TestExportSettingsRefuseAFontPath(t *testing.T) {
	for _, font := range []string{"/etc/passwd", `..\fonts\x.ttf`, "~/a.ttf"} {
		if _, err := settingsOf(t, map[string]string{"pptx_font": font}, nil); err == nil {
			t.Errorf("font %q was taken", font)
		}
	}
	if _, err := settingsOf(t, map[string]string{"docx_font": "PT Serif"}, nil); err != nil {
		t.Errorf("a family name was refused: %v", err)
	}
}

func TestExportSettingsRefuseAnOversizedTemplate(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(make([]byte, maxTemplateInflated+1)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	files := map[string][]memFile{"docx_template": {{Filename: "t.docx", Data: buf.Bytes()}}}
	if _, err := settingsOf(t, nil, files); err == nil {
		t.Fatal("a template that unpacks past the bound was taken")
	}
	notZip := map[string][]memFile{"pptx_template": {{Filename: "t.pptx", Data: []byte("text")}}}
	if _, err := settingsOf(t, nil, notZip); err == nil {
		t.Fatal("a template that is not a zip was taken")
	}
}
