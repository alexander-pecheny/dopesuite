package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// What the command looks like. Colour only when someone is watching: a pipe, a
// redirect or a CI log gets the same words with no escapes in them, so the
// output stays greppable and a script reading `Output: …` is unaffected.
//
// lipgloss decides colour support for itself, but not whether stdout is a
// terminal, so that is checked here and the styles fall back to plain.

var styled = isTerminal(os.Stdout)

var (
	styleOutput = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true) // green
	stylePath   = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))            // blue
	styleNote   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))           // grey
	styleWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))           // amber
	styleError  = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	styleDone   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
)

func paint(s lipgloss.Style, text string) string {
	if !styled {
		return text
	}
	return s.Render(text)
}

// reportOutput is the line every command ends a file with. Plain, it is
// chgksuite's own "Output: <path>", which a script may well be reading. On a
// terminal it becomes a tick and a dimmed directory, so a run that wrote a
// dozen files reads as a column of names.
func reportOutput(path string) {
	if !styled {
		fmt.Println("Output:", path)
		return
	}
	dir, name := filepath.Split(path)
	fmt.Println(styleOutput.Render("✓"), styleNote.Render(dir)+stylePath.Render(name))
}

// reportNote is a line about what the command is doing rather than what it made.
func reportNote(format string, a ...any) {
	fmt.Println(paint(styleNote, fmt.Sprintf(format, a...)))
}

// reportDone closes a run that had something to say about how it went.
func reportDone(format string, a ...any) {
	fmt.Println(paint(styleDone, fmt.Sprintf(format, a...)))
}

// warn goes to stderr: a font nobody has, a file that was skipped, a sandbox
// that had to be dropped.
func warn(format string, a ...any) {
	fmt.Fprintln(os.Stderr, paint(styleWarn, fmt.Sprintf(format, a...)))
}

// fail is how the command reports the error it is about to exit on.
func fail(err error) {
	fmt.Fprintln(os.Stderr, paint(styleError, "chgksuite:"), err)
}

func isTerminal(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// header titles a run that produces several files, so a batch is readable.
func header(text string) {
	if !styled {
		return
	}
	fmt.Println(lipgloss.NewStyle().Bold(true).Render(text))
}
