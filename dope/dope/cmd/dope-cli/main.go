// dope-cli — dope's API from the shell, for an agent or a script. It holds one
// server and one API token (ADR-0021) and sends whatever request it is given;
// the endpoints themselves are listed in the dope-api skill.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// state is what dope-cli remembers between calls: the server and the token.
type state struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	path  string
}

// statePath is $DOPE_CLI_STATE, else $XDG_CONFIG_HOME/dope-cli/state.json,
// else ~/.config/dope-cli/state.json.
func statePath() (string, error) {
	if p := os.Getenv("DOPE_CLI_STATE"); p != "" {
		return p, nil
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "dope-cli", "state.json"), nil
}

func loadState() (*state, error) {
	path, err := statePath()
	if err != nil {
		return nil, err
	}
	st := &state{path: path}
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(raw, st); err != nil {
			return nil, err
		}
	}
	if u := os.Getenv("DOPE_URL"); u != "" {
		st.URL = u
	}
	if t := os.Getenv("DOPE_TOKEN"); t != "" {
		st.Token = t
	}
	return st, nil
}

// save writes the state owner-readable only: the token is the account.
func (st *state) save() error {
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(st.path, append(raw, '\n'), 0o600)
}

type app struct {
	st     *state
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	http   *http.Client
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		for _, line := range []string{
			dopestrings.Default.Cli.Usage.Title(), "",
			dopestrings.Default.Cli.Usage.StartHead(), dopestrings.Default.Cli.Usage.StartLogin(), "",
			dopestrings.Default.Cli.Usage.CommandsHead(),
			dopestrings.Default.Cli.Usage.Login(), dopestrings.Default.Cli.Usage.Logout(), dopestrings.Default.Cli.Usage.Whoami(),
			dopestrings.Default.Cli.Usage.Fests(), dopestrings.Default.Cli.Usage.Fest(), dopestrings.Default.Cli.Usage.Api(), "",
			dopestrings.Default.Cli.Usage.FlagsHead(), dopestrings.Default.Cli.Usage.FlagFile(), dopestrings.Default.Cli.Usage.FlagOut(), "",
			dopestrings.Default.Cli.Usage.Env(),
		} {
			fmt.Fprintln(stdout, line)
		}
		return 0
	}
	st, err := loadState()
	if err != nil {
		fmt.Fprintln(stderr, dopestrings.Default.Cli.Run.StateUnreadable(err.Error()))
		return 1
	}
	a := &app{st: st, stdin: stdin, stdout: stdout, stderr: stderr, http: &http.Client{Timeout: 5 * time.Minute}}
	commands := map[string]func([]string) error{
		"login":  a.login,
		"logout": a.logout,
		"whoami": func([]string) error { return a.call("GET", "/api/auth/me", nil, "", "") },
		"fests":  func([]string) error { return a.call("GET", "/api/fests", nil, "", "") },
		"fest":   a.fest,
		"api":    a.api,
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintln(stderr, dopestrings.Default.Cli.Run.UnknownCommand(args[0]))
		return 2
	}
	if err := cmd(args[1:]); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stderr, "dope-cli:", err)
		}
		return 1
	}
	return 0
}

// parse reads flags wherever they stand among the positional arguments: an
// agent writes `api POST /api/… --file x.xlsx` as readily as the reverse.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
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

func (a *app) login(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	url := fs.String("url", "", "")
	token := fs.String("token", "", "")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if *url != "" {
		a.st.URL = strings.TrimRight(*url, "/")
	}
	if a.st.URL == "" {
		return corei18n.User(dopestrings.Default.Cli.Login.NeedUrl())
	}
	raw := *token
	if raw == "" {
		raw = os.Getenv("DOPE_TOKEN")
	}
	if raw == "" {
		fmt.Fprint(a.stderr, dopestrings.Default.Cli.Login.TokenPrompt())
		line, _ := bufio.NewReader(a.stdin).ReadString('\n')
		raw = line
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return corei18n.User(dopestrings.Default.Cli.Login.EmptyToken())
	}
	a.st.Token = raw
	var me struct {
		Username *string `json:"username"`
		UserID   int64   `json:"user_id"`
	}
	body, err := a.do("GET", "/api/auth/me", nil, "")
	if err == nil {
		err = json.Unmarshal(body, &me)
	}
	if err != nil {
		return corei18n.User(dopestrings.Default.Cli.Login.Rejected(err.Error()))
	}
	if err := a.st.save(); err != nil {
		return err
	}
	name := fmt.Sprintf("user-%d", me.UserID)
	if me.Username != nil {
		name = *me.Username
	}
	fmt.Fprintln(a.stdout, dopestrings.Default.Cli.Login.Done(name, a.st.URL, a.st.path))
	return nil
}

func (a *app) logout([]string) error {
	a.st.Token = ""
	if err := a.st.save(); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, dopestrings.Default.Cli.Logout.Done())
	return nil
}

func (a *app) fest(args []string) error {
	if len(args) != 1 {
		return corei18n.User(dopestrings.Default.Cli.Run.NeedRef())
	}
	return a.call("GET", "/api/fest/"+args[0]+"/settings", nil, "", "")
}

// api sends one request. The body is the argument itself, @file for a file's
// contents, or - for stdin; --file sends a file as a multipart form instead.
func (a *app) api(args []string) error {
	fs := flag.NewFlagSet("api", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	file := fs.String("file", "", "")
	out := fs.String("out", "", "")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) < 2 || len(rest) > 3 {
		return corei18n.User(dopestrings.Default.Cli.Run.ApiArgs())
	}
	method, path := strings.ToUpper(rest[0]), rest[1]
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	var body []byte
	contentType := ""
	switch {
	case *file != "":
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		part, err := mw.CreateFormFile("file", filepath.Base(*file))
		if err != nil {
			return err
		}
		data, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		if _, err := part.Write(data); err != nil {
			return err
		}
		if err := mw.Close(); err != nil {
			return err
		}
		body, contentType = buf.Bytes(), mw.FormDataContentType()
	case len(rest) == 3:
		arg := rest[2]
		switch {
		case arg == "-":
			body, err = io.ReadAll(a.stdin)
		case strings.HasPrefix(arg, "@"):
			body, err = os.ReadFile(arg[1:])
		default:
			body = []byte(arg)
		}
		if err != nil {
			return err
		}
		contentType = "application/json"
	}
	return a.call(method, path, body, contentType, *out)
}

// call sends a request and prints the answer: JSON indented, anything else
// as it came, or into the file out names.
func (a *app) call(method, path string, body []byte, contentType, out string) error {
	data, err := a.do(method, path, body, contentType)
	if err != nil {
		return err
	}
	if out != "" {
		return os.WriteFile(out, data, 0o644)
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, data, "", "  ") == nil {
		data = append(pretty.Bytes(), '\n')
	}
	_, err = a.stdout.Write(data)
	return err
}

// apiError is a refusal, carrying the server's own message, which dope writes
// for the person who caused it.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return fmt.Sprintf("%d %s", e.status, e.msg) }

func (a *app) do(method, path string, body []byte, contentType string) ([]byte, error) {
	if a.st.Token == "" {
		return nil, corei18n.User(dopestrings.Default.Cli.Run.NotLoggedIn())
	}
	if a.st.URL == "" {
		return nil, corei18n.User(dopestrings.Default.Cli.Login.NeedUrl())
	}
	req, err := http.NewRequest(method, a.st.URL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.st.Token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, &apiError{resp.StatusCode, dopestrings.Default.Cli.Run.TokenRejected()}
	}
	if resp.StatusCode >= 300 {
		return data, &apiError{resp.StatusCode, strings.TrimSpace(string(data))}
	}
	return data, nil
}
