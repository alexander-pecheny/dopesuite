// Command chgksuite-gui is a window over the chgksuite command line. It asks
// the tool what commands it has, draws a form for the one you pick, and runs
// it, streaming the output into the pane below.
//
// It is its own module so that xy, a server, never depends on a GUI toolkit,
// and it drives chgksuite as a subprocess rather than linking it, so a command
// that fails cannot take the window down with it.
package main

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

const logLines = 2000

type gui struct {
	app   fyne.App
	win   fyne.Window
	cli   string
	specs []commandSpec
	panes map[string]*pane
	cur   *pane

	body    *fyne.Container
	preview *widget.Label
	runBtn  *widget.Button
	stopBtn *widget.Button
	answer  *widget.Entry
	log     *widget.Label
	logBox  *container.Scroll
	lines   []string

	proc    *exec.Cmd
	stdin   io.WriteCloser
	picking bool
}

func main() {
	a := app.NewWithID("me.pecheny.chgksuite.gui")
	w := a.NewWindow("chgksuite")
	w.Resize(fyne.NewSize(1080, 800))
	g := &gui{app: a, win: w, panes: map[string]*pane{}}

	cli, err := findCLI()
	if err == nil {
		g.cli = cli
		g.specs, err = loadSpec(cli)
	}
	if err != nil {
		w.SetContent(container.NewCenter(widget.NewLabel(err.Error())))
	} else {
		g.build()
	}
	w.ShowAndRun()
}

func (g *gui) build() {
	tree := g.commandTree()

	g.body = container.NewStack()
	g.preview = widget.NewLabel("")
	g.preview.Wrapping = fyne.TextWrapWord
	g.preview.TextStyle = fyne.TextStyle{Monospace: true}
	g.runBtn = widget.NewButton("Run", g.start)
	g.runBtn.Importance = widget.HighImportance
	g.stopBtn = widget.NewButton("Stop", g.kill)
	g.stopBtn.Disable()

	g.log = widget.NewLabel("")
	g.log.TextStyle = fyne.TextStyle{Monospace: true}
	g.log.Wrapping = fyne.TextWrapWord
	g.logBox = container.NewVScroll(g.log)

	g.answer = widget.NewEntry()
	g.answer.SetPlaceHolder("what the command is waiting for, then Enter")
	g.answer.OnSubmitted = g.send
	g.answer.Disable()

	buttons := container.NewHBox(g.runBtn, g.stopBtn, widget.NewButton("Copy output", func() {
		g.win.Clipboard().SetContent(strings.Join(g.lines, "\n"))
	}), widget.NewButton("Clear output", func() { g.lines = nil; g.log.SetText("") }))

	top := container.NewBorder(nil, container.NewVBox(widget.NewSeparator(), g.preview, buttons), nil, nil, g.body)
	bottom := container.NewBorder(nil, g.answer, nil, nil, g.logBox)
	right := container.NewVSplit(top, bottom)
	split := container.NewHSplit(tree, right)

	// The dividers start where the widest command name needs them and stay
	// wherever they are dragged to.
	prefs := g.app.Preferences()
	split.Offset = prefs.FloatWithFallback("sidebar", 0.16)
	right.Offset = prefs.FloatWithFallback("logsplit", 0.6)
	g.win.SetOnClosed(func() {
		prefs.SetFloat("sidebar", split.Offset)
		prefs.SetFloat("logsplit", right.Offset)
	})

	g.win.SetContent(split)
	g.win.SetOnDropped(g.dropped)
	tree.OpenAllBranches()
	tree.Select(g.specs[0].Verb)
}

// commands is the command list the tree draws: grouped the way the usage
// screen groups them — compose, handouts, board — so a name is a word wide
// rather than a sentence.
type commands struct {
	roots    []string
	children map[string][]string
	byVerb   map[string]commandSpec
}

func group(specs []commandSpec) *commands {
	c := &commands{children: map[string][]string{}, byVerb: map[string]commandSpec{}}
	for _, spec := range specs {
		c.byVerb[spec.Verb] = spec
		head, _, grouped := strings.Cut(spec.Verb, " ")
		if !grouped {
			c.roots = append(c.roots, spec.Verb)
			continue
		}
		if _, seen := c.children[head]; !seen {
			c.roots = append(c.roots, head)
		}
		c.children[head] = append(c.children[head], spec.Verb)
	}
	return c
}

func (c *commands) childUIDs(uid string) []string {
	if uid == "" {
		return c.roots
	}
	return c.children[uid]
}

// isBranch is true of the empty root as well as of a group: Fyne walks the tree
// from the root down, and a root that says it is a leaf draws nothing at all.
func (c *commands) isBranch(uid string) bool {
	return uid == "" || len(c.children[uid]) > 0
}

func (c *commands) label(uid string, branch bool) string {
	if branch {
		return uid
	}
	if _, leaf, grouped := strings.Cut(uid, " "); grouped {
		return leaf
	}
	return uid
}

func (g *gui) commandTree() *widget.Tree {
	c := group(g.specs)
	tree := widget.NewTree(
		c.childUIDs,
		c.isBranch,
		func(bool) fyne.CanvasObject {
			label := widget.NewLabel("")
			label.Truncation = fyne.TextTruncateEllipsis
			return label
		},
		func(uid widget.TreeNodeID, branch bool, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(c.label(uid, branch))
		},
	)
	tree.OnSelected = func(uid widget.TreeNodeID) {
		if spec, ok := c.byVerb[uid]; ok {
			g.show(spec)
		}
	}
	return tree
}

func (g *gui) show(spec commandSpec) {
	p, ok := g.panes[spec.Verb]
	if !ok {
		p = g.buildPane(spec)
		g.panes[spec.Verb] = p
	}
	g.cur = p
	g.body.Objects = []fyne.CanvasObject{p.obj}
	g.body.Refresh()
	g.refresh()
}

func (g *gui) refresh() {
	if g.cur == nil {
		return
	}
	g.preview.SetText(previewLine(g.cli, g.cur.argv()))
}

// dropped adds what was dragged onto the window to the first file argument the
// command takes, which saves choosing a packet through a dialog.
func (g *gui) dropped(_ fyne.Position, uris []fyne.URI) {
	if g.cur == nil {
		return
	}
	for _, row := range g.cur.rows {
		if row.spec.Kind != "files" && row.spec.Kind != "file" {
			continue
		}
		for _, u := range uris {
			if row.spec.Kind == "file" {
				row.paths = []string{u.Path()}
			} else {
				row.paths = append(row.paths, u.Path())
			}
		}
		row.shown.SetText(row.describe())
		g.refresh()
		return
	}
}

func (g *gui) start() {
	if g.cur == nil || g.proc != nil {
		return
	}
	args := g.cur.argv()
	g.appendLine("$ " + previewLine(g.cli, args))

	cmd := exec.Command(g.cli, args...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		dialog.ShowError(err, g.win)
		return
	}
	read, write, err := os.Pipe()
	if err != nil {
		dialog.ShowError(err, g.win)
		return
	}
	cmd.Stdout, cmd.Stderr = write, write
	if err := cmd.Start(); err != nil {
		write.Close()
		read.Close()
		g.appendLine(err.Error())
		return
	}
	write.Close()
	g.proc, g.stdin = cmd, stdin
	g.running(true)

	go func() {
		scanner := bufio.NewScanner(read)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			fyne.Do(func() { g.appendLine(line) })
		}
		err := cmd.Wait()
		read.Close()
		fyne.Do(func() {
			if err != nil {
				g.appendLine("failed: " + err.Error())
			} else {
				g.appendLine("done")
			}
			g.proc, g.stdin = nil, nil
			g.running(false)
		})
	}()
}

func (g *gui) running(on bool) {
	for _, b := range []*widget.Button{g.runBtn, g.stopBtn} {
		b.Enable()
	}
	if on {
		g.runBtn.Disable()
		g.answer.Enable()
	} else {
		g.stopBtn.Disable()
		g.answer.Disable()
	}
}

func (g *gui) kill() {
	if g.proc != nil && g.proc.Process != nil {
		g.proc.Process.Kill()
	}
}

func (g *gui) send(text string) {
	if g.stdin == nil {
		return
	}
	io.WriteString(g.stdin, text+"\n")
	g.appendLine("> " + text)
	g.answer.SetText("")
}

func (g *gui) appendLine(line string) {
	g.lines = append(g.lines, line)
	if len(g.lines) > logLines {
		g.lines = g.lines[len(g.lines)-logLines:]
	}
	g.log.SetText(strings.Join(g.lines, "\n"))
	g.logBox.ScrollToBottom()
}

// previewLine is the command as it would be typed, with anything secret in it
// starred out.
func previewLine(cli string, args []string) string {
	out := []string{filepath.Base(cli)}
	mask := false
	for _, a := range args {
		if mask {
			out, mask = append(out, "********"), false
			continue
		}
		mask = strings.HasPrefix(a, "--") && secret(strings.TrimPrefix(a, "--"))
		out = append(out, quote(a))
	}
	return strings.Join(out, " ")
}

func quote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.ContainsAny(s, " \t'\"\\$*?") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}
