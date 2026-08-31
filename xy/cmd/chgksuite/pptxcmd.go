package main

import (
	"flag"
	"fmt"
	"os"

	"xy/internal/chgk/pptx"
)

// composePptx is `chgksuite compose pptx`: a package as the deck it is played
// from.
func composePptx(args []string) error {
	fs := flag.NewFlagSet("compose pptx", flag.ContinueOnError)
	configPath := fs.String("pptx_config", "", "a pptx_config.toml of your own; empty is the one chgksuite ships")
	template := fs.String("template", "", "a .pptx to build on; empty is chgksuite's own")
	fontDir := fs.String("font_dir", override("font_dir", ""), "an extra directory to look for the measurement font in")
	disableNumbers := fs.Bool("disable_numbers", false, "no question number in the corner")
	noAccents := fs.Bool("do_not_remove_accents", false, "keep the stress marks")
	language := languageFlag(fs)
	optimizeSize := fs.String("optimize_size", override("optimize_size", "on"), "re-encode the pictures to shrink the file: on|off")
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

	lang, labelsFile, err := language()
	if err != nil {
		return err
	}
	opts := pptx.Options{
		DisableNumbers:     *disableNumbers,
		DoNotRemoveAccents: *noAccents,
		Language:           lang,
		LabelsFile:         labelsFile,
		OptimizeSize:       *optimizeSize != "off",
		NoBreak:            noBreak(),
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

	sources, err := loadSources(fs.Args(), *merge)
	if err != nil {
		return err
	}
	for _, s := range sources {
		images, err := loadImages(s.doc, s.dir)
		if err != nil {
			return err
		}
		data, err := pptx.Export(s.doc, images, opts)
		if err != nil {
			return err
		}
		out := outputName(s.path, "pptx", "", *addTS == "on")
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return err
		}
		fmt.Println("Output:", out)
	}
	return nil
}
