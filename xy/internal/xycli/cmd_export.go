package xycli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// `source` prints the 4s a List (or its whole List Group) exports as — a whole
// tour as text, in one call. `export` hands that same source, plus the images it
// references, to the server, which renders the formats and streams back one file
// or a zip of them.

func cmdSource(a *app, args []string) error {
	fs := a.flags("source", "xy-cli source --board B --list L\n4s списка целиком: версии свёрнуты в один вопрос, как в экспорте.")
	board := a.boardFlag(fs)
	list := fs.Int64("list", 0, "id списка (группа берётся целиком)")
	_, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if *list == 0 {
		return errors.New("нужен --list")
	}
	_, b, err := a.open(*board)
	if err != nil {
		return err
	}
	title, cards, err := b.ListScope(*list)
	if err != nil {
		return err
	}
	source := ExportSource(descsOf(cards))
	return a.emit(map[string]any{"list_id": *list, "title": title, "source": source}, func() {
		a.printf("%s", source)
		a.note("# «%s», карточек: %d\n", title, len(cards))
	})
}

func descsOf(cards []Card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.Desc
	}
	return out
}

func cmdExport(a *app, args []string) error {
	fs := a.flags("export", "xy-cli export --board B --list L --format docx,pdf [--out каталог]\nФорматы: 4s, docx, pdf, pdf_mobile, handouts.")
	board := a.boardFlag(fs)
	list := fs.Int64("list", 0, "id списка (группа берётся целиком)")
	formats := fs.String("format", "docx", "форматы через запятую")
	out := fs.String("out", ".", "каталог для файлов")
	_, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if *list == 0 {
		return errors.New("нужен --list")
	}
	c, b, err := a.open(*board)
	if err != nil {
		return err
	}
	title, cards, err := b.ListScope(*list)
	if err != nil {
		return err
	}
	source := ExportSource(descsOf(cards))
	images, err := gatherImages(c, b, cards, source)
	if err != nil {
		return err
	}
	data, filename, err := c.ExportPack(source, safeName(title), *formats, images)
	if err != nil {
		return err
	}
	if filename == "" {
		filename = safeName(title) + ".bin"
	}
	path := filepath.Join(*out, filename)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	return a.emit(map[string]any{"path": path, "bytes": len(data)}, func() {
		a.printf("%s (%d байт)\n", path, len(data))
	})
}

// gatherImages downloads and decrypts the attachments the source's (img …)
// directives name, so the server renders the same pictures the browser would.
func gatherImages(c *Client, b *Board, cards []Card, source string) (map[string][]byte, error) {
	wanted := map[string]bool{}
	for _, name := range imageRefs(source) {
		wanted[name] = true
	}
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
				return nil, fmt.Errorf("вложение «%s»: %w", name, err)
			}
			plain, err := b.DK.DecBytes(raw)
			if err != nil {
				return nil, fmt.Errorf("вложение «%s»: %w", name, err)
			}
			images[name] = plain
		}
	}
	return images, nil
}

// imageRefs collects the (img …) filenames a 4s text references. As in
// chgksuite's parseimg the filename is the last whitespace token — the rest are
// w=/h=/big/inline options — and a reference inside a hidden comment is not one,
// since nothing renders it.
func imageRefs(source string) []string {
	var out []string
	seen := map[string]bool{}
	for _, directive := range directives(source) {
		if strings.HasPrefix(directive, "hidden-comment") {
			continue
		}
		rest, ok := strings.CutPrefix(directive, "img ")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		if name := fields[len(fields)-1]; !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// directives returns the body of every top-level (…) run, brackets balanced so a
// ")" inside a filename does not end one early.
func directives(s string) []string {
	var out []string
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '(' {
			continue
		}
		depth, start := 1, i+1
		j := start
		for ; j < len(runes) && depth > 0; j++ {
			switch runes[j] {
			case '(':
				depth++
			case ')':
				depth--
			}
		}
		if depth == 0 {
			out = append(out, string(runes[start:j-1]))
			i = j - 1
		}
	}
	return out
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
