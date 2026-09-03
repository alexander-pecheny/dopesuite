package xycli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	xystrings "xy/i18nstrings"
)

// The command layer. xy-cli is meant to be driven by an agent, so: human text by
// default and --json where a machine wants exactness, a board named on every
// command (no hidden "current board"), and Card content as raw 4s in and out.

type app struct {
	st     *State
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	json   bool
}

type command struct {
	name    string
	summary string
	run     func(a *app, args []string) error
}

var commands = []command{
	{"login", xystrings.Default.Cli.Login.Summary(), cmdLogin},
	{"logout", xystrings.Default.Cli.Logout.Summary(), cmdLogout},
	{"boards", xystrings.Default.Cli.Boards.Summary(), cmdBoards},
	{"unlock", xystrings.Default.Cli.Unlock.Summary(), cmdUnlock},
	{"lock", xystrings.Default.Cli.Lock.Summary(), cmdLock},
	{"board", xystrings.Default.Cli.Board.Summary(), cmdBoard},
	{"list", xystrings.Default.Cli.List.Summary(), cmdList},
	{"card", xystrings.Default.Cli.Card.Summary(), cmdCard},
	{"comment", xystrings.Default.Cli.Comment.Summary(), cmdComment},
	{"label", xystrings.Default.Cli.Label.Summary(), cmdLabel},
	{"search", xystrings.Default.Cli.Search.Summary(), cmdSearch},
	{"source", xystrings.Default.Cli.Source.Summary(), cmdSource},
	{"export", xystrings.Default.Cli.Export.Summary(), cmdExport},
	{"attachment", xystrings.Default.Cli.Attachment.Summary(), cmdAttachment},
}

// Run is the whole CLI: cmd/xy-cli is a main() around it.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	s := xystrings.Default
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage(stdout)
		return 0
	}
	st, err := LoadState()
	if err != nil {
		fmt.Fprintln(stderr, s.Cli.Run.StateUnreadable(err.Error()))
		return 1
	}
	a := &app{st: st, stdin: stdin, stdout: stdout, stderr: stderr}
	for _, c := range commands {
		if c.name != args[0] {
			continue
		}
		if err := c.run(a, args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 2
			}
			fmt.Fprintln(stderr, "xy-cli:", err)
			return 1
		}
		return 0
	}
	fmt.Fprint(stderr, s.Cli.Run.UnknownCommand(args[0]))
	usage(stderr)
	return 2
}

func usage(w io.Writer) {
	s := xystrings.Default
	fmt.Fprintln(w, s.Cli.Run.Title())
	fmt.Fprintln(w, s.Cli.Run.StartHead())
	fmt.Fprintln(w, s.Cli.Run.ExampleLogin())
	fmt.Fprintln(w, s.Cli.Run.ExampleBoards())
	fmt.Fprintln(w, s.Cli.Run.ExampleUnlock())
	fmt.Fprintln(w, s.Cli.Run.CommandsHead())
	for _, c := range commands {
		fmt.Fprintf(w, "  %-11s %s\n", c.name, c.summary)
	}
	fmt.Fprintln(w, s.Cli.Run.FooterHelp())
	fmt.Fprintln(w, s.Cli.Run.Footer4s())
}

// ---- shared plumbing ----

// flags builds a FlagSet that prints to the app's stderr and carries --json.
func (a *app) flags(name string, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	s := xystrings.Default
	fs.BoolVar(&a.json, "json", false, s.Cli.Run.JsonFlag())
	fs.Usage = func() {
		fmt.Fprint(a.stderr, s.Cli.Run.FlagsHead(name, usage))
		fs.PrintDefaults()
	}
	return fs
}

// boardFlag registers --board, which every content command takes: there is no
// current board, so each says which one it means.
func (a *app) boardFlag(fs *flag.FlagSet) *string {
	return fs.String("board", "", xystrings.Default.Cli.Shared.BoardFlag())
}

// oneArg parses flags and requires exactly one positional argument — the id of
// the thing acted on.
func (a *app) oneArg(fs *flag.FlagSet, args []string, what string) (string, error) {
	rest, err := a.parse(fs, args)
	if err != nil {
		return "", err
	}
	if len(rest) != 1 {
		return "", fmt.Errorf("нужен id: %s", what)
	}
	return rest[0], nil
}

// oneID is oneArg for the usual case: a numeric id.
func (a *app) oneID(fs *flag.FlagSet, args []string, what string) (int64, error) {
	arg, err := a.oneArg(fs, args, what)
	if err != nil {
		return 0, err
	}
	return parseID(arg, what)
}

// dispatch routes a command's verbs, so each family names its actions once.
func dispatch(family string, verbs map[string]func(*app, []string) error, a *app, args []string) error {
	names := make([]string, 0, len(verbs))
	for name := range verbs {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(args) == 0 {
		return fmt.Errorf("%s %s", family, strings.Join(names, "|"))
	}
	verb, ok := verbs[args[0]]
	if !ok {
		return fmt.Errorf("неизвестное действие %q (%s)", args[0], strings.Join(names, ", "))
	}
	return verb(a, args[1:])
}

// pickOne resolves a user's reference — an id, or a name matched the forgiving
// way a search matches — against a list of named things.
func pickOne[T any](items []T, ref, what string, id func(T) int64, name func(T) string) (T, error) {
	var zero T
	var found []T
	for _, item := range items {
		if itoa(id(item)) == ref || strings.EqualFold(name(item), ref) {
			return item, nil
		}
		if strings.Contains(Fold(name(item)), Fold(ref)) {
			found = append(found, item)
		}
	}
	if len(found) == 1 {
		return found[0], nil
	}
	if len(found) == 0 {
		return zero, fmt.Errorf("%s %q не найдено", what, ref)
	}
	labels := make([]string, len(found))
	for i, item := range found {
		labels[i] = fmt.Sprintf("%d %s", id(item), name(item))
	}
	return zero, fmt.Errorf("под %q подходит несколько: %s", ref, strings.Join(labels, ", "))
}

// parse handles flags and positional arguments in any order — an agent writes
// `card get 412 --board 3`, and flag.Parse stops at the first non-flag.
func (a *app) parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func (a *app) client() (*Client, error) {
	if a.st.URL == "" || a.st.Token == "" {
		return nil, errors.New("сначала `xy-cli login` (токен создаётся на /profile/tokens)")
	}
	return NewClient(a.st.URL, a.st.Token), nil
}

// boardRef resolves --board: an id, or a name matched against the boards whose
// key this machine holds. Only those, because without the key nothing on the
// board can be read anyway.
func (a *app) boardRef(ref string) (int64, DataKey, error) {
	if ref == "" {
		return 0, nil, errors.New("нужен --board <id|имя>")
	}
	held := make([]HeldKey, 0, len(a.st.Boards))
	for idStr, board := range a.st.Boards {
		board.id, _ = strconv.ParseInt(idStr, 10, 64)
		held = append(held, board)
	}
	sort.Slice(held, func(i, j int) bool { return held[i].id < held[j].id })
	board, err := pickOne(held, ref, xystrings.Default.Cli.Shared.WhatUnlockedBoard(),
		func(h HeldKey) int64 { return h.id }, func(h HeldKey) string { return h.Name })
	if err != nil {
		if id, convErr := strconv.ParseInt(ref, 10, 64); convErr == nil {
			return 0, nil, fmt.Errorf("ключа доски %d нет: `xy-cli unlock %d`", id, id)
		}
		return 0, nil, fmt.Errorf("%w — см. `xy-cli boards`", err)
	}
	dk, _ := a.st.Key(board.id)
	return board.id, dk, nil
}

// open is what almost every command starts with: the client, and the board
// named by --board, decrypted.
func (a *app) open(ref string) (*Client, *Board, error) {
	c, err := a.client()
	if err != nil {
		return nil, nil, err
	}
	id, dk, err := a.boardRef(ref)
	if err != nil {
		return nil, nil, err
	}
	b, err := LoadBoard(c, dk, id)
	if err != nil {
		return nil, nil, err
	}
	return c, b, nil
}

// emit prints the JSON value when --json was asked for; otherwise it runs the
// human rendering.
func (a *app) emit(value any, human func()) error {
	if !a.json {
		human()
		return nil
	}
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", " ")
	return enc.Encode(value)
}

func (a *app) printf(format string, args ...any) {
	fmt.Fprintf(a.stdout, format, args...)
}

func (a *app) note(format string, args ...any) {
	fmt.Fprintf(a.stderr, format, args...)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func parseID(s, what string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: нужен числовой id, а не %q", what, s)
	}
	return id, nil
}

// readText takes the text a write command writes: --text if given, else stdin.
func (a *app) readText(text string, file string) (string, error) {
	if text != "" {
		return text, nil
	}
	if file != "" && file != "-" {
		raw, err := os.ReadFile(file)
		return string(raw), err
	}
	raw, err := io.ReadAll(a.stdin)
	return string(raw), err
}
