package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	xystrings "xy/i18nstrings"
	"xy/internal/chgk/board"
	"xy/internal/chgk/docx"
)

// board runs `chgksuite board <subcommand>` (`trello` is the old name for it).
func boardCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("board needs a subcommand (token, download, upload)")
	}
	switch args[0] {
	case "token":
		return boardToken(args[1:])
	case "download":
		return boardDownload(args[1:])
	case "upload":
		return boardUpload(args[1:])
	default:
		return fmt.Errorf("board %s is not a subcommand", args[0])
	}
}

// boardToken mints or stores the credential for a service: Trello hands one out
// through its connect page, xy through /profile/tokens.
func boardToken(args []string) error {
	fs := flag.NewFlagSet("board token", flag.ContinueOnError)
	noBrowser := fs.Bool("no-browser", false, "print the URL instead of opening it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	serviceURL := "https://trello.com"
	if fs.NArg() > 0 {
		serviceURL = fs.Arg(0)
	}
	host := board.ServiceHost(serviceURL)
	if host == "trello.com" {
		if *noBrowser {
			fmt.Println(xystrings.Default.Chgkcli.Board.OpenBrowser(), board.TrelloConnectURL)
		} else {
			openBrowser(board.TrelloConnectURL)
		}
	} else {
		base := strings.TrimSuffix(withScheme(serviceURL), "/")
		fmt.Print(xystrings.Default.Chgkcli.Board.OpenBrowserTokens(base))
	}
	token, err := prompt(xystrings.Default.Chgkcli.Board.TokenPrompt())
	if err != nil {
		return err
	}
	if token == "" {
		return fmt.Errorf("%s", xystrings.Default.Chgkcli.Board.EmptyToken())
	}
	return board.SetTokenFor(host, token)
}

func boardDownload(args []string) error {
	fs := flag.NewFlagSet("board download", flag.ContinueOnError)
	lists := fs.String("lists", "", "download only these lists, comma-separated")
	si := fs.Bool("si", false, "also write a .docx per list, with the card captions as headings")
	qb := fs.String("qb", "", "pair two lists into a quizbowl .docx: --qb tossups,bonuses")
	onlyAnswers := fs.Bool("onlyanswers", false, xystrings.Default.Chgkcli.Board.OnlyAnswersFlag())
	noAnswers := fs.Bool("noanswers", false, xystrings.Default.Chgkcli.Board.NoAnswersFlag())
	singleFile := fs.Bool("singlefile", false, "one .4s for the whole board")
	labels := fs.Bool("labels", false, "also write a file per label")
	replaceBreaks := fs.Bool("replace_double_line_breaks", false, "collapse the double line breaks Trello's editor added")
	fixEditor := fs.String("fix_trello_new_editor", override("fix_trello_new_editor", "on"),
		"undo what Trello's 2023 editor did to a description: on|off")
	font := fs.String("font", override("font", ""), "font family for the .docx outputs")
	docxTemplate := fs.String("docx_template", "", "a .docx to build the .docx outputs on")
	config := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := applyConfig(fs, *config); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("board download takes the folder to synchronise")
	}
	folder := fs.Arg(0)

	b, err := resolveBoard(folder)
	if err != nil {
		return err
	}
	opts := board.DownloadOptions{
		SI: *si, OnlyAnswers: *onlyAnswers, NoAnswers: *noAnswers,
		SingleFile: *singleFile, Labels: *labels,
		ReplaceDoubleLineBreaks: *replaceBreaks,
		FixTrelloNewEditor:      *fixEditor == "on",
	}
	if *lists != "" {
		opts.Lists = strings.Split(*lists, ",")
	}
	if *qb != "" {
		opts.QB = strings.Split(*qb, ",")
		if len(opts.QB) != 2 {
			return fmt.Errorf("--qb takes two list names, comma-separated")
		}
	}
	if opts.Docx, err = boardDocxOptions(*font, *docxTemplate); err != nil {
		return err
	}

	client := board.NewClient()
	j, err := client.Fetch(context.Background(), b)
	if err != nil {
		return err
	}
	files, err := board.Download(j, opts)
	if err != nil {
		return err
	}
	for _, f := range files {
		out := filepath.Join(folder, f.Name)
		if err := os.WriteFile(out, f.Data, 0o644); err != nil {
			return err
		}
		reportOutput(out)
	}
	return nil
}

func boardUpload(args []string) error {
	fs := flag.NewFlagSet("board upload", flag.ContinueOnError)
	author := fs.Bool("author", false, "put the author in the card's caption too")
	listName := fs.String("list_name", "", "the list to upload into; empty is the board's first")
	config := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := applyConfig(fs, *config); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("board upload takes a board URL and the files to send")
	}
	b, err := board.ParseURL(fs.Arg(0))
	if err != nil {
		return err
	}
	if err := attachToken(&b); err != nil {
		return err
	}
	client := board.NewClient()
	if b.Service == board.XY {
		if err := unlockXY(client, &b, ""); err != nil {
			return err
		}
	}
	files, err := sourcesToUpload(fs.Args()[1:])
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("%s", xystrings.Default.Chgkcli.Board.NothingToUpload())
	}
	ctx := context.Background()
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		header(path)
		if err := client.Upload(ctx, b, string(src), *listName, *author, func(s string) {
			reportNote("  %s", s)
		}); err != nil {
			return err
		}
	}
	return nil
}

// sourcesToUpload expands a directory argument into the package files in it, as
// gui_board_upload does.
func sourcesToUpload(args []string) ([]string, error) {
	var out []string
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			out = append(out, arg)
			continue
		}
		entries, err := os.ReadDir(arg)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() && isPackageFile(e.Name()) {
				out = append(out, filepath.Join(arg, e.Name()))
			}
		}
	}
	return out, nil
}

func isPackageFile(name string) bool {
	for _, ext := range []string{".4s", ".si4s", ".br4s", ".tr4s"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

// resolveBoard is resolve_download_board: the folder remembers its board, and
// the first run asks.
func resolveBoard(folder string) (board.Board, error) {
	meta, ok, err := board.ReadMetadata(folder)
	if err != nil {
		return board.Board{}, err
	}
	if !ok {
		fmt.Println(xystrings.Default.Chgkcli.Board.NeedBoardUrl())
		fmt.Println("  https://trello.com/b/Bi0z2H49/title-of-your-board")
		fmt.Println("  https://xy.pecheny.me/board/2")
		fmt.Println()
		url, err := prompt(xystrings.Default.Chgkcli.Board.BoardUrlPrompt())
		if err != nil {
			return board.Board{}, err
		}
		meta.BoardURL = url
	}
	b, err := board.ParseURL(meta.BoardURL)
	if err != nil {
		return board.Board{}, err
	}
	b.Passphrase = meta.Passphrase
	if err := attachToken(&b); err != nil {
		return board.Board{}, err
	}
	if b.Service == board.XY && b.Passphrase == "" {
		if b.Passphrase, err = prompt(xystrings.Default.Chgkcli.Board.InitialPassphrasePrompt()); err != nil {
			return board.Board{}, err
		}
	}
	if !ok {
		if err := board.WriteMetadata(folder, board.Metadata{BoardURL: meta.BoardURL, Passphrase: b.Passphrase}); err != nil {
			return board.Board{}, err
		}
	}
	return b, nil
}

// attachToken is _attach_token: the saved credential for the board's host, or a
// message saying how to get one.
func attachToken(b *board.Board) error {
	token, err := board.TokenFor(b.Host)
	if err != nil {
		return err
	}
	if token == "" {
		return fmt.Errorf("%s", xystrings.Default.Chgkcli.Board.NoToken(b.Host, b.BaseURL))
	}
	b.Token = token
	if b.Service == board.Trello {
		b.Key = board.TrelloAppKey
	}
	return nil
}

// unlockXY asks for the passphrase an xy board needs and derives its data key.
func unlockXY(c *board.Client, b *board.Board, passphrase string) error {
	km, err := c.FetchKeymeta(context.Background(), *b)
	if err != nil {
		return err
	}
	if passphrase == "" {
		passphrase = b.Passphrase
	}
	if passphrase == "" {
		if passphrase, err = prompt(xystrings.Default.Chgkcli.Board.PassphrasePrompt()); err != nil {
			return err
		}
	}
	b.Passphrase = passphrase
	return c.Unlock(passphrase, km)
}

func boardDocxOptions(font, template string) (docx.Options, error) {
	o := docx.Options{Font: font}
	if template != "" {
		raw, err := os.ReadFile(template)
		if err != nil {
			return o, err
		}
		o.Template = raw
	}
	return o, nil
}

func prompt(question string) (string, error) {
	fmt.Print(question)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func withScheme(s string) string {
	if strings.Contains(s, "://") {
		return s
	}
	return "https://" + s
}

// openBrowser is webbrowser.open, for the one page Trello mints a token on.
func openBrowser(url string) {
	var cmd string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "explorer"
	default:
		cmd = "xdg-open"
	}
	if err := exec.Command(cmd, url).Start(); err != nil {
		fmt.Println(xystrings.Default.Chgkcli.Board.OpenBrowser(), url)
	}
}
