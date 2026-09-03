package xycli

import (
	"encoding/json"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"fmt"
	corei18n "pecheny.me/dopecore/i18nstrings"
	xystrings "xy/i18nstrings"
)

// Attachments are ciphertext on the server like everything else: the CLI seals
// what it uploads and opens what it downloads, and never asks the server to see
// either.

func cmdAttachment(a *app, args []string) error {
	return dispatch("attachment", map[string]func(*app, []string) error{
		"ls": attachmentList, "get": attachmentGet, "add": attachmentAdd,
	}, a, args)
}

func attachmentList(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("attachment ls", s.Cli.Attachment.LsUsage())
	board := a.boardFlag(fs)
	cardID, err := a.oneID(fs, args, s.Cli.Shared.WhatCard())
	if err != nil {
		return err
	}
	c, b, err := a.open(*board)
	if err != nil {
		return err
	}
	atts, err := c.Attachments(cardID)
	if err != nil {
		return err
	}
	type row struct {
		ID       int64  `json:"id"`
		Filename string `json:"filename"`
		Mime     string `json:"mime"`
		Size     int64  `json:"size"`
	}
	rows := []row{}
	for _, att := range atts {
		name, err := b.DK.DecField(att.FilenameEnc)
		if err != nil {
			name = s.Cli.Attachment.UnreadableName()
		}
		rows = append(rows, row{ID: att.ID, Filename: name, Mime: att.Mime, Size: att.Size})
	}
	return a.emit(rows, func() {
		for _, r := range rows {
			a.printf("%6d  %-40s %8d  %s\n", r.ID, r.Filename, r.Size, r.Mime)
		}
	})
}

func attachmentGet(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("attachment get", s.Cli.Attachment.GetUsage())
	board := a.boardFlag(fs)
	card := fs.Int64("card", 0, s.Cli.Attachment.GetCardFlag())
	out := fs.String("o", "", s.Cli.Attachment.GetOutFlag())
	attID, err := a.oneID(fs, args, s.Cli.Shared.WhatAttachment())
	if err != nil {
		return err
	}
	c, b, err := a.open(*board)
	if err != nil {
		return err
	}
	name := ""
	if *card != 0 {
		atts, err := c.Attachments(*card)
		if err != nil {
			return err
		}
		for _, att := range atts {
			if att.ID == attID {
				if decoded, err := b.DK.DecField(att.FilenameEnc); err == nil {
					name = decoded
				}
			}
		}
	}
	raw, err := c.AttachmentBytes(attID)
	if err != nil {
		return err
	}
	plain, err := b.DK.DecBytes(raw)
	if err != nil {
		return corei18n.User(s.Cli.Attachment.DecryptFailed(itoa(attID), err.Error()))
	}
	path := *out
	if path == "" {
		if name == "" {
			name = fmt.Sprintf("attachment-%d.bin", attID)
		}
		path = safeName(name)
	}
	if err := os.WriteFile(path, plain, 0o644); err != nil {
		return err
	}
	return a.emit(map[string]any{"path": path, "bytes": len(plain)}, func() {
		a.printf("%s", s.Cli.Attachment.GetDone(path, strconv.Itoa(len(plain))))
	})
}

func attachmentAdd(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("attachment add", s.Cli.Attachment.AddUsage())
	board := a.boardFlag(fs)
	name := fs.String("name", "", s.Cli.Attachment.AddNameFlag())
	rest, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return corei18n.User(s.Cli.Attachment.NeedCardFile())
	}
	cardID, err := parseID(rest[0], s.Cli.Shared.WhatCard())
	if err != nil {
		return err
	}
	path := rest[1]
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	filename := *name
	if filename == "" {
		filename = filepath.Base(path)
	}
	c, b, err := a.open(*board)
	if err != nil {
		return err
	}
	if _, err := b.Card(cardID); err != nil {
		return err
	}
	sealed, err := b.DK.EncBytes(raw)
	if err != nil {
		return err
	}
	filenameEnc, err := b.DK.EncField(filename)
	if err != nil {
		return err
	}
	// The timeline entry that says an attachment arrived, in the browser's shape.
	event, err := json.Marshal(map[string]string{"file": filename})
	if err != nil {
		return err
	}
	eventEnc, err := b.DK.EncField(string(event))
	if err != nil {
		return err
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	id, err := c.UploadAttachment(cardID, map[string]any{
		"filename_enc": filenameEnc, "mime": mimeType, "event_payload_enc": eventEnc,
	}, sealed)
	if err != nil {
		return err
	}
	return a.emit(map[string]any{"id": id, "filename": filename}, func() {
		a.printf("%s", s.Cli.Attachment.Added(itoa(id), filename, itoa(cardID)))
	})
}
