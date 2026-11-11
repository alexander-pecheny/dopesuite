package main

import (
	"encoding/json"
	"fmt"
	"os"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/credits"
)

// `chgksuite question` prints, as JSON, one question of a packet in 4s: the
// one whose number, as the packet writes it, equals --number. It is for the
// rating site, which shows an appealed question to the appeal jury, so like
// `credits` it is not in the usage table.
//
//	chgksuite question --number 7 [--name packet.zip] [--questions 36] <file | ->
//
// The file is read as `credits` reads it (formats, the packet picked out of an
// archive). The output carries the pictures the question names that the file
// holds (.docx, .doc, .rtf, PDF — the PDF's through poppler's pdftohtml — or
// beside a .4s in an archive; raster only, base64). No question
// under that number, or two, exits with code 4; a password-protected file with
// code 3.
func questionCmd(args []string) error {
	fs := newFlagSet("question")
	number := fs.String("number", "", "the question's number as the packet writes it")
	name := fs.String("name", "", "the packet's original file name; its extension picks the format")
	questions := fs.Int("questions", 0, "how many questions the tournament has; picks the packet out of a zip")
	pdftotext := fs.String("pdftotext", os.Getenv("CHGKSUITE_PDFTOTEXT"), `the pdftotext binary PDFs are read with; "pdftotext" on PATH when empty`)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 || *number == "" {
		return fmt.Errorf("question takes --number and one file, or - for stdin")
	}
	in := fs.Arg(0)
	data, err := readInput(in)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = in
	}
	q, err := credits.Question(*name, data, *number, credits.Options{Questions: *questions, PDFToText: *pdftotext})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(q)
}
