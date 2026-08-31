package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"xy/internal/chgk/docx"
	"xy/internal/chgk/fsource"
)

// compose runs `chgksuite compose <filetype> …`.
func compose(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("compose needs a filetype (docx, pptx, telegram, markdown, redditmd, base, openquiz)")
	}
	switch args[0] {
	case "docx":
		return composeDocx(args[1:])
	case "telegram":
		return composeTelegram(args[1:])
	case "markdown", "redditmd", "base", "openquiz":
		return composePublished(args[0], args[1:])
	case "pptx":
		return composePptx(args[1:])
	default:
		return fmt.Errorf("compose %s is not ported yet", args[0])
	}
}

func composeDocx(args []string) error {
	fs := flag.NewFlagSet("compose docx", flag.ContinueOnError)
	spoilers := fs.String("spoilers", "off", "hide answers: off|whiten|pagebreak|dots")
	screenMode := fs.String("screen_mode", "off", "export for screen: off|replace_all|add_versions|add_versions_columns")
	noAnswers := fs.Bool("noanswers", false, "do not print answers")
	noParagraph := fs.Bool("noparagraph", false, `no line break after "Вопрос N."`)
	onlyNumber := fs.Bool("only_question_number", false, `label questions "N." instead of "Вопрос N."`)
	smallerSource := fs.String("smaller_source_and_author", "on", "set source and author 2pt below the body: on|off")
	randomize := fs.Bool("randomize", false, "shuffle the questions")
	addTS := fs.String("add_ts", "off", "append a timestamp to the output filename: on|off")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts, err := validDocxOptions(docx.Options{
		Spoilers:                docx.Spoilers(*spoilers),
		ScreenMode:              docx.ScreenMode(*screenMode),
		NoAnswers:               *noAnswers,
		NoParagraph:             *noParagraph,
		OnlyQuestionNumber:      *onlyNumber,
		SameSourceAndAuthorSize: *smallerSource == "off",
	})
	if err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("no input file")
	}
	for _, in := range fs.Args() {
		out, err := composeDocxFile(in, opts, *randomize, *addTS == "on")
		if err != nil {
			return err
		}
		fmt.Println("Output:", out)
	}
	return nil
}

func validDocxOptions(o docx.Options) (docx.Options, error) {
	switch o.Spoilers {
	case docx.SpoilersOff, docx.SpoilersWhiten, docx.SpoilersPagebreak, docx.SpoilersDots:
	default:
		return o, fmt.Errorf("unknown --spoilers %q", o.Spoilers)
	}
	switch o.ScreenMode {
	case docx.ScreenOff, docx.ScreenReplaceAll, docx.ScreenAddVersions, docx.ScreenAddVersionsColumns:
	default:
		return o, fmt.Errorf("unknown --screen_mode %q", o.ScreenMode)
	}
	return o, nil
}

func composeDocxFile(in string, opts docx.Options, randomize, addTS bool) (string, error) {
	src, err := os.ReadFile(in)
	if err != nil {
		return "", err
	}
	doc := fsource.Parse(string(src), gameOf(in))
	if randomize {
		fsource.Randomize(doc, rand.New(rand.NewSource(time.Now().UnixNano())))
	}
	images, err := loadImages(doc, filepath.Dir(in))
	if err != nil {
		return "", err
	}
	data, err := docx.Export(doc, images, opts)
	if err != nil {
		return "", err
	}
	out := outputName(in, "docx", docxSuffix(opts), addTS)
	return out, os.WriteFile(out, data, 0o644)
}

// docxSuffix is chgksuite's addsuffix: the export's switches, in its filename.
func docxSuffix(o docx.Options) string {
	s := ""
	switch o.ScreenMode {
	case docx.ScreenReplaceAll:
		s = "_screen"
	case docx.ScreenAddVersions, docx.ScreenAddVersionsColumns:
		s = "_screen_versions"
	}
	if o.Spoilers != "" && o.Spoilers != docx.SpoilersOff {
		s += "_spoilers"
	}
	return s
}

// outputName ports make_filename: the input's basename, the switches' suffix,
// an optional timestamp, and the new extension, beside the input.
func outputName(in, ext, suffix string, addTS bool) string {
	base := strings.TrimSuffix(filepath.Base(in), filepath.Ext(in)) + suffix
	if addTS {
		base += time.Now().Format("_20060102T1504")
	}
	return filepath.Join(filepath.Dir(in), base+"."+ext)
}

// gameOf reads the game from the extension, as ext_to_game does.
func gameOf(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".si4s":
		return "si"
	case ".br4s":
		return "brain"
	case ".tr4s":
		return "troika"
	default:
		return "chgk"
	}
}
