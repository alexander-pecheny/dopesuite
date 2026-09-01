package main

import (
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// A pane is one command's form: a row per positional argument, then a row per
// flag, with the widget chosen by what the flag says it takes.

type pane struct {
	spec   commandSpec
	rows   []*inputRow
	values map[string]string
	obj    fyne.CanvasObject
}

type inputRow struct {
	spec  input
	paths []string
	text  string
	shown *widget.Label
}

func (r *inputRow) args() []string {
	switch r.spec.Kind {
	case "files", "file", "folder":
		return r.paths
	default:
		if r.text == "" {
			return nil
		}
		return []string{r.text}
	}
}

func (r *inputRow) describe() string {
	switch len(r.paths) {
	case 0:
		if r.spec.Optional {
			return "nothing chosen; the command's own default is used"
		}
		return "nothing chosen"
	case 1:
		return r.paths[0]
	default:
		names := make([]string, len(r.paths))
		for i, p := range r.paths {
			names[i] = filepath.Base(p)
		}
		return strings.Join(names, ", ")
	}
}

func (g *gui) buildPane(spec commandSpec) *pane {
	p := &pane{spec: spec, values: map[string]string{}}
	box := container.NewVBox(
		widget.NewLabelWithStyle(spec.Verb, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel(spec.What),
	)
	for _, in := range spec.Inputs {
		row := &inputRow{spec: in}
		p.rows = append(p.rows, row)
		box.Add(g.inputWidget(p, row))
	}
	if len(spec.Inputs) > 0 {
		box.Add(widget.NewSeparator())
	}
	var plain, folded []*widget.FormItem
	for _, f := range spec.Flags {
		p.values[f.Name] = f.Default
		if f.Advanced {
			folded = append(folded, g.flagItem(p, f))
		} else {
			plain = append(plain, g.flagItem(p, f))
		}
	}
	if len(plain) > 0 {
		box.Add(widget.NewForm(plain...))
	}
	if len(folded) > 0 {
		box.Add(widget.NewAccordion(widget.NewAccordionItem("Advanced", widget.NewForm(folded...))))
	}
	p.obj = container.NewVScroll(box)
	return p
}

func (g *gui) inputWidget(p *pane, row *inputRow) fyne.CanvasObject {
	label := widget.NewLabel(row.spec.Label)
	label.TextStyle = fyne.TextStyle{Bold: true}
	switch row.spec.Kind {
	case "text":
		entry := widget.NewEntry()
		entry.SetPlaceHolder(row.spec.Label)
		entry.OnChanged = func(s string) { row.text = s; g.refresh() }
		return container.NewBorder(nil, nil, label, nil, entry)
	case "choice":
		group := widget.NewRadioGroup(row.spec.Choices, func(s string) { row.text = s; g.refresh() })
		group.Horizontal = true
		return container.NewBorder(nil, nil, label, nil, group)
	}
	row.shown = widget.NewLabel(row.describe())
	row.shown.Wrapping = fyne.TextWrapWord
	update := func(paths []string, add bool) {
		if add {
			row.paths = append(row.paths, paths...)
		} else {
			row.paths = paths
		}
		row.shown.SetText(row.describe())
		g.refresh()
	}
	var buttons *fyne.Container
	switch row.spec.Kind {
	case "folder":
		buttons = container.NewHBox(widget.NewButton("Choose folder…", func() {
			g.pickFolder(func(path string) { update([]string{path}, false) })
		}))
	case "file":
		buttons = container.NewHBox(widget.NewButton("Choose…", func() {
			g.pickFiles(row.spec.Ext, false, func(paths []string) { update(paths, false) })
		}))
	default:
		buttons = container.NewHBox(
			widget.NewButton("Add…", func() {
				g.pickFiles(row.spec.Ext, true, func(paths []string) { update(paths, true) })
			}),
			widget.NewButton("Clear", func() { update(nil, false) }),
		)
	}
	return container.NewBorder(nil, nil, label, buttons, row.shown)
}

func (g *gui) flagItem(p *pane, f flagSpec) *widget.FormItem {
	var w fyne.CanvasObject
	set := func(v string) { p.values[f.Name] = v; g.refresh() }
	switch {
	case f.Bool:
		check := widget.NewCheck("", func(on bool) {
			set(map[bool]string{true: "true", false: "false"}[on])
		})
		check.SetChecked(f.Default == "true")
		w = check
	case len(f.Choices) > 4:
		sel := widget.NewSelect(f.Choices, set)
		sel.SetSelected(f.Default)
		w = sel
	case len(f.Choices) > 0:
		group := widget.NewRadioGroup(f.Choices, set)
		group.Horizontal = true
		group.SetSelected(f.Default)
		w = group
	case f.Path != "":
		entry := widget.NewEntry()
		entry.SetText(f.Default)
		entry.OnChanged = set
		choose := widget.NewButton("…", func() {
			if f.Path == "folder" {
				g.pickFolder(func(path string) { entry.SetText(path) })
			} else {
				g.pickFiles(nil, false, func(paths []string) { entry.SetText(paths[0]) })
			}
		})
		w = container.NewBorder(nil, nil, nil, choose, entry)
	case secret(f.Name):
		entry := widget.NewPasswordEntry()
		entry.OnChanged = set
		w = entry
	default:
		entry := widget.NewEntry()
		entry.SetText(f.Default)
		entry.OnChanged = set
		w = entry
	}
	item := widget.NewFormItem(f.Name, w)
	item.HintText = f.Usage
	return item
}

func secret(name string) bool {
	return strings.Contains(name, "password") || strings.Contains(name, "token")
}

// argv is the command line the form adds up to. A flag left at its default is
// left out, so what runs reads like what someone would have typed.
func (p *pane) argv() []string {
	args := strings.Fields(p.spec.Verb)
	for _, f := range p.spec.Flags {
		v := p.values[f.Name]
		if v == f.Default || v == "" {
			continue
		}
		if f.Bool {
			if v == "true" {
				args = append(args, "--"+f.Name)
			}
			continue
		}
		args = append(args, "--"+f.Name, v)
	}
	for _, row := range p.rows {
		args = append(args, row.args()...)
	}
	return args
}
