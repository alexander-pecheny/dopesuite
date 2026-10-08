package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// Some commands ask a question before they run, and the CLI's spec says which
// ones and how (its confirm field): a check to run on the command's file, the
// answer that makes the question unnecessary, the flags that skip the check, and
// the flag to add once the person says yes. Today that is the telegram export
// asking about a packet without stats. Nothing here names a command or a flag.

// confirmSubject is the file a run would check, or "" when the run is not
// checked: the command asks nothing, a SkipIf flag is set, or no file is
// chosen yet.
func confirmSubject(p *pane, args []string) string {
	c := p.spec.Confirm
	if c == nil {
		return ""
	}
	for _, name := range c.SkipIf {
		if slices.Contains(args, "--"+name) {
			return ""
		}
	}
	for _, row := range p.rows {
		if row.spec.Kind == "file" && len(row.paths) == 1 {
			return row.paths[0]
		}
	}
	return ""
}

// runCheck runs the spec's check on the file and reads its answer.
func runCheck(cli string, c *confirmSpec, file string) (bool, error) {
	args := append(slices.Clone(c.Check), file)
	name := strings.Join(c.Check, " ")
	out, err := exec.Command(cli, args...).Output()
	if err != nil {
		return false, fmt.Errorf("%s: %w", name, err)
	}
	var answer map[string]bool
	if err := json.Unmarshal(out, &answer); err != nil {
		return false, fmt.Errorf("%s: %w", name, err)
	}
	ok, found := answer[c.Field]
	if !found {
		return false, fmt.Errorf("%s: no %q in its answer", name, c.Field)
	}
	return ok, nil
}

// withFlag is the command line with --flag added straight after the verb,
// ahead of the file, which is where the flag package stops reading flags.
func withFlag(verb string, args []string, flag string) []string {
	if slices.Contains(args, "--"+flag) {
		return args
	}
	return slices.Insert(slices.Clone(args), len(strings.Fields(verb)), "--"+flag)
}

// confirmThenRun runs args, first asking the spec's question when the check
// says it is needed. A check that fails is logged and the question is asked
// anyway: not knowing is no reason to skip it.
func (g *gui) confirmThenRun(args []string, run func([]string)) {
	file := confirmSubject(g.cur, args)
	if file == "" {
		run(args)
		return
	}
	spec := g.cur.spec
	check := g.check
	if check == nil {
		check = runCheck
	}
	ok, err := check(g.cli, spec.Confirm, file)
	if err != nil {
		g.appendLine(err.Error())
	}
	if ok && err == nil {
		run(args)
		return
	}
	ask := g.ask
	if ask == nil {
		ask = g.askConfirm
	}
	ask(spec.Confirm, func(yes bool) {
		if yes {
			run(withFlag(spec.Verb, args, spec.Confirm.Flag))
		}
	})
}

// askConfirm is the question as a dialog. "No" is the highlighted button and
// "yes" a plain one, so the eye lands on the safe answer.
func (g *gui) askConfirm(c *confirmSpec, answer func(bool)) {
	msg := widget.NewLabel(c.Question)
	msg.Wrapping = fyne.TextWrapWord
	d := dialog.NewCustomWithoutButtons(c.Title, msg, g.win)
	no := widget.NewButton(c.No, func() { d.Hide(); answer(false) })
	no.Importance = widget.HighImportance
	yes := widget.NewButton(c.Yes, func() { d.Hide(); answer(true) })
	d.SetButtons([]fyne.CanvasObject{no, yes})
	d.Show()
}
