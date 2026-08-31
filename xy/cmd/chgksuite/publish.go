package main

import (
	"flag"
	"fmt"
	"os"

	"xy/internal/chgk/dbtext"
	"xy/internal/chgk/imghost"
	"xy/internal/chgk/markdown"
	"xy/internal/chgk/openquiz"
)

// composePublished runs the three exports that publish a package as text —
// markdown, redditmd, base and openquiz — which is what they have in common:
// a picture becomes a URL rather than embedded bytes, so each takes an image
// host (imgur, as chgksuite does).
func composePublished(filetype string, args []string) error {
	fs := flag.NewFlagSet("compose "+filetype, flag.ContinueOnError)
	clientID := fs.String("imgur_client_id", override("imgur_client_id", ""), "upload pictures as this imgur client instead of chgksuite's")
	removeAccents := fs.Bool("remove_accents", false, "base only: a stressed vowel becomes a capital one")
	addTS := fs.String("add_ts", override("add_ts", "off"), "append a timestamp to the output filename: on|off")
	merge := fs.Bool("merge", false, "export the input files as one package")
	noBreak := noBreakFlags(fs)
	config := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := applyConfig(fs, *config); err != nil {
		return err
	}
	sources, err := loadSources(fs.Args(), *merge)
	if err != nil {
		return err
	}
	host := imghost.NewImgur(*clientID)
	for _, s := range sources {
		doc := s.doc
		images, err := loadImages(doc, s.dir)
		if err != nil {
			return err
		}

		var data []byte
		ext := "md"
		switch filetype {
		case "markdown", "redditmd":
			text, err := markdown.Export(doc, images, host, markdown.Options{Reddit: filetype == "redditmd"})
			if err != nil {
				return err
			}
			data = []byte(text)
		case "base":
			text, err := dbtext.Export(doc, images, host, dbtext.Options{RemoveAccents: *removeAccents})
			if err != nil {
				return err
			}
			data, ext = []byte(text), "txt"
		case "openquiz":
			if data, err = openquiz.Export(doc, images, host, openquiz.Options{NoBreak: noBreak()}); err != nil {
				return err
			}
			ext = "json"
		}
		out := outputName(s.path, ext, "", *addTS == "on")
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return err
		}
		fmt.Println("Output:", out)
	}
	return nil
}
