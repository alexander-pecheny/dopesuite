package xycli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	xystrings "xy/i18nstrings"

	"golang.org/x/term"
)

// login stores the instance and an API token minted in the browser at
// /profile/tokens. The password never reaches the CLI: the token is a
// month-lived, revocable credential, and changing the account password revokes
// every one of them at once (ADR-0015).
func cmdLogin(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("login", s.Cli.Login.Usage())
	url := fs.String("url", "", s.Cli.Login.UrlFlag())
	token := fs.String("token", "", s.Cli.Login.TokenFlag())
	_, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	base := *url
	if base == "" {
		base = a.st.URL
	}
	if base == "" {
		base = os.Getenv("XY_URL")
	}
	if base == "" {
		return errors.New("нужен --url (или XY_URL)")
	}
	raw := *token
	if raw == "" {
		raw = os.Getenv("XY_TOKEN")
	}
	if raw == "" {
		var err error
		if raw, err = a.secret(s.Cli.Login.TokenPrompt()); err != nil {
			return err
		}
	}
	if raw == "" {
		return errors.New("пустой токен")
	}

	c := NewClient(base, raw)
	username, err := c.Me()
	if err != nil {
		return fmt.Errorf("токен не принят: %w", err)
	}
	a.st.URL, a.st.Token = strings.TrimRight(base, "/"), raw
	if err := a.st.Save(); err != nil {
		return err
	}
	path, _ := StatePath()
	return a.emit(map[string]any{"url": a.st.URL, "username": username}, func() {
		a.printf("%s", s.Cli.Login.Done(username, a.st.URL, path))
	})
}

func cmdLogout(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("logout", s.Cli.Logout.Usage())
	_, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	a.st.Token = ""
	a.st.Boards = map[string]HeldKey{}
	if err := a.st.Save(); err != nil {
		return err
	}
	return a.emit(map[string]any{"ok": true}, func() { a.printf("%s", s.Cli.Logout.Done()) })
}

type boardRow struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Unlocked bool   `json:"unlocked"`
}

func cmdBoards(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("boards", s.Cli.Boards.Usage())
	_, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	c, err := a.client()
	if err != nil {
		return err
	}
	boards, err := c.Boards()
	if err != nil {
		return err
	}
	rows := make([]boardRow, 0, len(boards))
	for _, b := range boards {
		dk, held := a.st.Key(b.ID)
		name := b.Name
		if name == "" && held {
			// A legacy board (schema_version 1) still keeps its name encrypted.
			if decoded, err := dk.DecField(b.NameEnc); err == nil {
				name = decoded
			}
		}
		rows = append(rows, boardRow{ID: b.ID, Name: name, Role: b.Role, Unlocked: held})
	}
	return a.emit(rows, func() {
		for _, r := range rows {
			lock := "🔒"
			if r.Unlocked {
				lock = "🔓"
			}
			a.printf("%6d  %s  %s\n", r.ID, lock, r.Name)
		}
	})
}

// unlock is the one place a board passphrase is typed. The unwrapped data key
// is written to the state file so later commands need nobody at the keyboard
// (ADR-0016).
func cmdUnlock(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("unlock", s.Cli.Unlock.Usage())
	rest, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errors.New("нужен id или имя доски: xy-cli unlock 12")
	}
	c, err := a.client()
	if err != nil {
		return err
	}
	boards, err := c.Boards()
	if err != nil {
		return err
	}
	board, err := matchBoard(boards, rest[0])
	if err != nil {
		return err
	}
	km, err := c.Keymeta(board.ID)
	if err != nil {
		return err
	}
	pass, err := a.secret(s.Cli.Unlock.PassphrasePrompt(board.Name))
	if err != nil {
		return err
	}
	dk, err := Unlock(pass, km)
	if err != nil {
		return err
	}
	name := board.Name
	if name == "" {
		if decoded, err := dk.DecField(board.NameEnc); err == nil {
			name = decoded
		}
	}
	a.st.Hold(board.ID, name, dk)
	if err := a.st.Save(); err != nil {
		return err
	}
	return a.emit(map[string]any{"id": board.ID, "name": name}, func() {
		a.printf("%s", s.Cli.Unlock.Done(itoa(board.ID), name))
	})
}

func cmdLock(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("lock", s.Cli.Lock.Usage())
	all := fs.Bool("all", false, s.Cli.Lock.AllFlag())
	rest, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if *all {
		a.st.Boards = map[string]HeldKey{}
		if err := a.st.Save(); err != nil {
			return err
		}
		return a.emit(map[string]any{"ok": true}, func() { a.printf("%s", s.Cli.Lock.AllDone()) })
	}
	if len(rest) != 1 {
		return errors.New("нужен id или имя доски (или --all)")
	}
	id, _, err := a.boardRef(rest[0])
	if err != nil {
		return err
	}
	name := a.st.Boards[itoa(id)].Name
	a.st.Forget(id)
	if err := a.st.Save(); err != nil {
		return err
	}
	return a.emit(map[string]any{"id": id}, func() { a.printf("%s", s.Cli.Lock.Done(itoa(id), name)) })
}

// matchBoard resolves an id or a name against what the account can see — the
// unlock path, where the key is not held yet so boardRef cannot help.
func matchBoard(boards []BoardSummary, ref string) (BoardSummary, error) {
	return pickOne(boards, ref, xystrings.Default.Cli.Shared.WhatBoard(),
		func(b BoardSummary) int64 { return b.ID }, func(b BoardSummary) string { return b.Name })
}

// secret reads a passphrase or token: without echo from a terminal, from stdin
// when there is none (so a setup script can pipe one in).
func (a *app) secret(prompt string) (string, error) {
	if f, ok := a.stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(a.stderr, prompt)
		raw, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(a.stderr)
		return strings.TrimSpace(string(raw)), err
	}
	line, err := bufio.NewReader(a.stdin).ReadString('\n')
	return strings.TrimSpace(line), err
}
