package main

import (
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"github.com/ncruces/zenity"
)

// Choosing a file goes through the system's own panel: the one people know,
// and the only one that takes several files at once. zenity draws it with
// osascript on macOS and the Win32 dialogs on Windows, both of which are always
// there. On Linux it wants the zenity or kdialog binary, and where there is
// none, Fyne's own dialog below stands in.

func (g *gui) pickFiles(exts []string, multiple bool, done func([]string)) {
	if !zenity.IsAvailable() {
		g.fyneFile(exts, func(path string) { done([]string{path}) })
		return
	}
	opts := []zenity.Option{zenity.Title("Choose a file"), zenity.Filename(g.lastDir())}
	if len(exts) > 0 {
		opts = append(opts, zenity.FileFilters{{Patterns: patterns(exts), CaseFold: true}})
	}
	g.panel(func() ([]string, error) {
		if multiple {
			return zenity.SelectFileMultiple(opts...)
		}
		return one(zenity.SelectFile(opts...))
	}, func(paths []string) {
		g.remember(filepath.Dir(paths[0]))
		done(paths)
	})
}

func (g *gui) pickFolder(done func(string)) {
	if !zenity.IsAvailable() {
		g.fyneFolder(done)
		return
	}
	g.panel(func() ([]string, error) {
		return one(zenity.SelectFile(zenity.Directory(), zenity.Title("Choose a folder"), zenity.Filename(g.lastDir())))
	}, func(paths []string) {
		g.remember(paths[0])
		done(paths[0])
	})
}

// panel opens one off the UI goroutine, since it blocks until the panel closes,
// and brings the answer back onto it. A cancelled panel answers nothing.
func (g *gui) panel(open func() ([]string, error), done func([]string)) {
	if g.picking {
		return
	}
	g.picking = true
	go func() {
		paths, err := open()
		fyne.Do(func() {
			g.picking = false
			if err != nil || len(paths) == 0 {
				return
			}
			done(paths)
		})
	}()
}

func one(path string, err error) ([]string, error) {
	if path == "" {
		return nil, err
	}
	return []string{path}, err
}

func (g *gui) lastDir() string {
	dir := g.app.Preferences().String("lastdir")
	if dir == "" {
		return ""
	}
	return dir + string(filepath.Separator)
}

func (g *gui) remember(dir string) { g.app.Preferences().SetString("lastdir", dir) }

// patterns turns the extensions a command reads into the globs a filter wants.
func patterns(exts []string) []string {
	out := make([]string, len(exts))
	for i, ext := range exts {
		out[i] = "*" + ext
	}
	return out
}

func (g *gui) fyneFile(exts []string, done func(string)) {
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil || r == nil {
			return
		}
		defer r.Close()
		path := r.URI().Path()
		g.app.Preferences().SetString("lastdir", filepath.Dir(path))
		done(path)
	}, g.win)
	if len(exts) > 0 {
		d.SetFilter(storage.NewExtensionFileFilter(exts))
	}
	g.openDialog(d)
}

func (g *gui) fyneFolder(done func(string)) {
	d := dialog.NewFolderOpen(func(l fyne.ListableURI, err error) {
		if err != nil || l == nil {
			return
		}
		g.app.Preferences().SetString("lastdir", l.Path())
		done(l.Path())
	}, g.win)
	g.openDialog(d)
}

// openDialog opens a picker where the last one was left. The resize has to come
// after Show: FileDialog.Resize measures a window that Show is what creates,
// and reaches through the nil pointer before then.
func (g *gui) openDialog(d *dialog.FileDialog) {
	if dir := g.app.Preferences().String("lastdir"); dir != "" {
		if l, err := storage.ListerForURI(storage.NewFileURI(dir)); err == nil {
			d.SetLocation(l)
		}
	}
	d.Show()
	d.Resize(fyne.NewSize(880, 620))
}
