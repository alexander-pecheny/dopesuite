package main

import (
	"slices"
	"testing"

	"fyne.io/fyne/v2/test"
)

var docx = commandSpec{
	Verb:   "compose docx",
	What:   "render questions to .docx",
	Inputs: []input{{Kind: "files", Label: "packets", Ext: []string{".4s"}}},
	Flags: []flagSpec{
		{Name: "language", Default: "ru", Choices: []string{"ru", "en", "ua", "by", "uz", "sr"}},
		{Name: "merge", Default: "false", Bool: true},
		{Name: "spoilers", Default: "off", Choices: []string{"off", "whiten", "pagebreak", "dots"}},
		{Name: "font", Default: ""},
	},
}

func pane1(t *testing.T) (*gui, *pane) {
	t.Helper()
	test.NewApp()
	g := &gui{panes: map[string]*pane{}}
	return g, g.buildPane(docx)
}

func TestArgvLeavesOutWhatWasNotTouched(t *testing.T) {
	_, p := pane1(t)
	if got := p.argv(); !slices.Equal(got, []string{"compose", "docx"}) {
		t.Errorf("untouched form gave %q", got)
	}
}

func TestArgvCarriesWhatWasChanged(t *testing.T) {
	_, p := pane1(t)
	p.values["spoilers"] = "dots"
	p.values["merge"] = "true"
	p.values["font"] = "PT Sans"
	p.rows[0].paths = []string{"/tmp/a.4s", "/tmp/b.4s"}
	want := []string{"compose", "docx", "--merge", "--spoilers", "dots", "--font", "PT Sans", "/tmp/a.4s", "/tmp/b.4s"}
	if got := p.argv(); !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPreviewQuotesAndStarsOutSecrets(t *testing.T) {
	got := previewLine("/usr/local/bin/chgksuite", []string{"compose", "lj", "--password", "hunter2", "/tmp/a b.4s"})
	want := "chgksuite compose lj --password ******** '/tmp/a b.4s'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
