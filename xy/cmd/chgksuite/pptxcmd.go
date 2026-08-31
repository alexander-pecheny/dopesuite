package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/pptx"
)

// composePptx is `chgksuite compose pptx`: a package as the deck it is played
// from.
func composePptx(args []string) error {
	fs := flag.NewFlagSet("compose pptx", flag.ContinueOnError)
	configPath := fs.String("pptx_config", "", "a pptx_config.toml of your own; empty is the one chgksuite ships")
	template := fs.String("template", "", "a .pptx to build on; empty is chgksuite's own")
	fontDir := fs.String("font_dir", "", "an extra directory to look for the measurement font in")
	disableNumbers := fs.Bool("disable_numbers", false, "no question number in the corner")
	noAccents := fs.Bool("do_not_remove_accents", false, "keep the stress marks")
	language := fs.String("language", "ru", "the language runs are tagged with")
	addTS := fs.String("add_ts", "off", "append a timestamp to the output filename: on|off")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("no input file")
	}

	opts := pptx.Options{
		DisableNumbers:     *disableNumbers,
		DoNotRemoveAccents: *noAccents,
		Language:           *language,
	}
	if *fontDir != "" {
		opts.FontDirs = []string{*fontDir}
	}
	if *configPath != "" {
		raw, err := os.ReadFile(*configPath)
		if err != nil {
			return err
		}
		if opts.Config, err = pptx.ParseConfig(string(raw)); err != nil {
			return fmt.Errorf("%s: %w", *configPath, err)
		}
	}
	if *template != "" {
		raw, err := os.ReadFile(*template)
		if err != nil {
			return err
		}
		opts.Template = raw
	}

	for _, in := range fs.Args() {
		src, err := os.ReadFile(in)
		if err != nil {
			return err
		}
		doc := fsource.Parse(string(src), gameOf(in))
		images, err := loadImages(doc, filepath.Dir(in))
		if err != nil {
			return err
		}
		data, err := pptx.Export(doc, images, opts)
		if err != nil {
			return err
		}
		out := outputName(in, "pptx", "", *addTS == "on")
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return err
		}
		fmt.Println("Output:", out)
	}
	return nil
}
