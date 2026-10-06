package xycli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	xystrings "xy/i18nstrings"

	"xy/internal/chgk/inline"
	"xy/internal/listexport"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// `source` prints the 4s a List (or its whole List Group) exports as — a whole
// tour as text, in one call. `export` hands that same source, its .hndt and the
// images both reference, to the server, which renders the formats and streams
// back one file or a zip of them. Both are assembled by internal/listexport.

func cmdSource(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("source", s.Cli.Source.Usage())
	board := a.boardFlag(fs)
	list := fs.Int64("list", 0, s.Cli.Source.ListFlag())
	_, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if *list == 0 {
		return corei18n.User(s.Cli.Source.NeedList())
	}
	_, b, err := a.open(*board)
	if err != nil {
		return err
	}
	title, cards, err := b.ListScope(*list)
	if err != nil {
		return err
	}
	source := listexport.Assemble(exportCards(cards)).Source
	return a.emit(map[string]any{"list_id": *list, "title": title, "source": source}, func() {
		a.printf("%s", source)
		a.note("%s", s.Cli.Source.Note(title, strconv.Itoa(len(cards))))
	})
}

func cmdExport(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("export", s.Cli.Export.Usage())
	board := a.boardFlag(fs)
	list := fs.Int64("list", 0, s.Cli.Export.ListFlag())
	formats := fs.String("format", "docx", s.Cli.Export.FormatFlag())
	out := fs.String("out", ".", s.Cli.Export.OutFlag())
	_, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if *list == 0 {
		return corei18n.User(s.Cli.Export.NeedList())
	}
	c, b, err := a.open(*board)
	if err != nil {
		return err
	}
	title, cards, err := b.ListScope(*list)
	if err != nil {
		return err
	}
	assembly := listexport.Assemble(exportCards(cards))
	images, err := gatherImages(c, b, cards, assembly.Source)
	if err != nil {
		return err
	}
	data, filename, err := c.ExportPack(assembly, safeName(title), *formats, images)
	if err != nil {
		return err
	}
	if filename == "" {
		filename = safeName(title) + ".bin"
	}
	path := filepath.Join(*out, filename)
	if err := os.WriteFile(path, data, outputFileMode); err != nil {
		return err
	}
	return a.emit(map[string]any{"path": path, "bytes": len(data)}, func() {
		a.printf("%s", s.Cli.Export.Done(path, strconv.Itoa(len(data))))
	})
}

// gatherImages downloads and decrypts the attachments the source's (img …)
// directives name, so the server renders the same pictures the browser would.
// inline.ImageRefs finds them the way the browser's imgRefs does.
func gatherImages(c *Client, b *Board, cards []Card, source string) (map[string][]byte, error) {
	wanted := map[string]bool{}
	for _, name := range inline.ImageRefs(source) {
		wanted[name] = true
	}
	s := xystrings.Default
	if len(wanted) == 0 {
		return nil, nil
	}
	images := map[string][]byte{}
	for _, card := range cards {
		atts, err := c.Attachments(card.ID)
		if err != nil {
			return nil, err
		}
		for _, att := range atts {
			name, err := b.DK.DecField(att.FilenameEnc)
			if err != nil || !wanted[name] || images[name] != nil {
				continue
			}
			raw, err := c.AttachmentBytes(att.ID)
			if err != nil {
				return nil, corei18n.User(s.Cli.Export.AttachmentError(name, err.Error()))
			}
			plain, err := b.DK.DecBytes(raw)
			if err != nil {
				return nil, corei18n.User(s.Cli.Export.AttachmentError(name, err.Error()))
			}
			images[name] = plain
		}
	}
	return images, nil
}

// safeName keeps an export's filename usable on any filesystem.
func safeName(title string) string {
	cleaned := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(title))
	if cleaned == "" {
		return "export"
	}
	return cleaned
}
