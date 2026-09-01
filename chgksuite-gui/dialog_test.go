package main

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// The fallback picker, for a Linux with neither zenity nor kdialog. Opening it
// used to take the whole window down with it: the dialog was
// resized before it was shown, and Fyne measures a window that Show is what
// creates. Nothing here asserts — it crashes, or it does not.
func TestOpeningAPickerDoesNotCrash(t *testing.T) {
	a := test.NewApp()
	w := test.NewWindow(nil)
	w.Resize(fyne.NewSize(900, 700))
	g := &gui{app: a, win: w, panes: map[string]*pane{}}

	g.fyneFile([]string{".4s"}, func(string) {})
	g.fyneFolder(func(string) {})

	g.app.Preferences().SetString("lastdir", t.TempDir())
	g.fyneFile(nil, func(string) {})
	g.fyneFolder(func(string) {})
}

func TestPatternsAreGlobs(t *testing.T) {
	got := patterns([]string{".4s", ".si4s"})
	if len(got) != 2 || got[0] != "*.4s" || got[1] != "*.si4s" {
		t.Errorf("patterns gave %q", got)
	}
}
