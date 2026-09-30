package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"xy/internal/chgk/credits"
)

// `chgksuite credits` prints, as JSON, who a packet credits: its tours with
// their question numbers, every meta and editor-line paragraph with its
// position, and each question's author field — no other question field
// (internal/chgk/credits says what it reads). It is for the rating site, which
// turns this into player links, so like `spec` it is not in the usage table.
//
//	chgksuite credits [--name packet.zip] [--questions 36] <file | ->
//
// --name gives the format when the file itself has no telling extension, as with
// a stored upload or stdin. --questions is the tournament's question count, which
// picks the packet out of a zip's other documents.
func creditsCmd(args []string) error {
	fs := newFlagSet("credits")
	name := fs.String("name", "", "the packet's original file name; its extension picks the format")
	questions := fs.Int("questions", 0, "how many questions the tournament has; picks the packet out of a zip")
	pdftotext := fs.String("pdftotext", os.Getenv("CHGKSUITE_PDFTOTEXT"), `the pdftotext binary PDFs are read with; "pdftotext" on PATH when empty`)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("credits takes one file, or - for stdin")
	}
	in := fs.Arg(0)
	var data []byte
	var err error
	if in == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(in)
	}
	if err != nil {
		return err
	}
	if *name == "" {
		*name = in
	}
	c, err := credits.Read(*name, data, credits.Options{Questions: *questions, PDFToText: *pdftotext})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(c)
}
