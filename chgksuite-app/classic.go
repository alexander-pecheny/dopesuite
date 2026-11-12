package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The classic tab's events: each line the command prints, then how it ended
// ("" when it succeeded).
const (
	lineEvent = "classic:line"
	doneEvent = "classic:done"
)

// readBuf is how much of the CLI's output is read at a time.
const readBuf = 64 << 10

// cliName is the CLI the app ships beside its own executable. It cannot be
// plain chgksuite, which is the app's own name.
const cliName = "chgksuite-cli"

// Classic is the service behind the classic tab: a form over the chgksuite
// CLI, drawn from what `chgksuite-cli spec` says about its commands. The CLI
// runs as a subprocess, so Stop can kill it and it can ask for input.
type Classic struct {
	cli     string
	typst   string
	window  *application.WebviewWindow
	lastDir string

	mu    sync.Mutex
	proc  *exec.Cmd
	stdin io.WriteCloser
}

func newClassic(typst string) *Classic {
	return &Classic{cli: findCLI(), typst: typst}
}

// findCLI is $CHGKSUITE_CLI, the CLI beside this executable, or the one on
// PATH, or "" when there is none.
func findCLI() string {
	if p := os.Getenv("CHGKSUITE_CLI"); p != "" {
		return p
	}
	name := cliName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	p, _ := exec.LookPath(name)
	return p
}

// Spec is the CLI's `spec`, which the page draws its form from.
func (c *Classic) Spec() (json.RawMessage, error) {
	if c.cli == "" {
		return nil, fmt.Errorf("не нашёл %s рядом с приложением", cliName)
	}
	out, err := exec.Command(c.cli, "spec").Output()
	if err != nil {
		return nil, fmt.Errorf("%s spec: %w", c.cli, err)
	}
	return out, nil
}

// Run starts the CLI with args and streams what it prints as events. It
// returns once the command has started.
func (c *Classic) Run(args []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.proc != nil {
		return errors.New("команда ещё работает")
	}
	cmd := exec.Command(c.cli, args...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	if c.typst != "" && os.Getenv("CHGKSUITE_TYPST") == "" {
		cmd.Env = append(cmd.Env, "CHGKSUITE_TYPST="+c.typst)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	read, write, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = write, write
	err = cmd.Start()
	write.Close()
	if err != nil {
		read.Close()
		return err
	}
	c.proc, c.stdin = cmd, stdin
	c.emit(lineEvent, "$ "+commandLine(args))
	go c.stream(cmd, read)
	return nil
}

// stream sends the output a line at a time. A read that leaves a line
// unfinished means the CLI has stopped to wait, as after a prompt, so that
// part goes out too.
func (c *Classic) stream(cmd *exec.Cmd, read *os.File) {
	buf := make([]byte, readBuf)
	var pending []byte
	for {
		n, err := read.Read(buf)
		pending = append(pending, buf[:n]...)
		for {
			i := bytes.IndexByte(pending, '\n')
			if i < 0 {
				break
			}
			c.emit(lineEvent, string(pending[:i]))
			pending = pending[i+1:]
		}
		if len(pending) > 0 && (n < len(buf) || err != nil) {
			c.emit(lineEvent, string(pending))
			pending = nil
		}
		if err != nil {
			break
		}
	}
	err := cmd.Wait()
	read.Close()
	c.mu.Lock()
	c.proc, c.stdin = nil, nil
	c.mu.Unlock()
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	c.emit(doneEvent, msg)
}

func (c *Classic) emit(name, data string) { application.Get().Event.Emit(name, data) }

// Stop kills the running command.
func (c *Classic) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.proc != nil {
		c.proc.Process.Kill()
	}
}

// Send types a line into the running command, which may be asking for a
// token or a board's passphrase.
func (c *Classic) Send(text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil {
		return errors.New("команда не запущена")
	}
	_, err := io.WriteString(c.stdin, text+"\n")
	return err
}

// PickFiles asks for one file, or for several, among those with exts.
func (c *Classic) PickFiles(exts []string, multiple bool) ([]string, error) {
	d := c.dialog().CanChooseFiles(true)
	if len(exts) > 0 {
		globs := make([]string, len(exts))
		for i, e := range exts {
			globs[i] = "*" + e
		}
		d.AddFilter(strings.Join(exts, ", "), strings.Join(globs, ";"))
	}
	var paths []string
	var err error
	if multiple {
		paths, err = d.PromptForMultipleSelection()
	} else {
		var p string
		p, err = d.PromptForSingleSelection()
		if p != "" {
			paths = []string{p}
		}
	}
	if len(paths) > 0 {
		c.lastDir = filepath.Dir(paths[0])
	}
	return paths, err
}

// PickFolder asks for a folder.
func (c *Classic) PickFolder() (string, error) {
	p, err := c.dialog().CanChooseDirectories(true).CanCreateDirectories(true).PromptForSingleSelection()
	if p != "" {
		c.lastDir = p
	}
	return p, err
}

// dialog opens where the last one was left.
func (c *Classic) dialog() *application.OpenFileDialogStruct {
	d := application.Get().Dialog.OpenFile().AttachToWindow(c.window)
	if c.lastDir != "" {
		d.SetDirectory(c.lastDir)
	}
	return d
}

// commandLine is the command as it would be typed, with any secret starred
// out, for the log.
func commandLine(args []string) string {
	out := []string{cliName}
	mask := false
	for _, a := range args {
		if mask {
			out, mask = append(out, "********"), false
			continue
		}
		name := strings.TrimPrefix(a, "--")
		mask = name != a && (strings.Contains(name, "password") || strings.Contains(name, "token"))
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
