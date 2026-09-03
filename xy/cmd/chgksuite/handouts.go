package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	xystrings "xy/i18nstrings"
	"xy/internal/chgk/fsource"
	"xy/internal/chgk/handout"
	"xy/internal/chgk/htmlshot"
	"xy/internal/chgk/i18n"
	"xy/internal/chgk/typstinstall"
)

// handouts runs `chgksuite handouts <subcommand>`.
func handouts(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("handouts needs a subcommand (generate, run, split_fit, pack)")
	}
	switch args[0] {
	case "generate", "4s2hndt":
		return handoutsGenerate(args[1:])
	case "run", "hndt2pdf":
		return handoutsRun(args[1:])
	case "split_fit":
		return handoutsSplitFit(args[1:])
	case "pack":
		return handoutsPack(args[1:])
	case "create_html":
		return handoutsCreateHTML(args[1:])
	case "html2img":
		return handoutsHTML2Img(args[1:])
	case "install":
		return handoutsInstall(args[1:])
	default:
		return fmt.Errorf("handouts %s is not ported yet", args[0])
	}
}

// handoutsGenerate is 4s2hndt: a package's handout brackets
// into the .hndt the renderer reads.
func handoutsGenerate(args []string) error {
	fs := flag.NewFlagSet("handouts generate", flag.ContinueOnError)
	language := fs.String("language", override("language", i18n.DefaultLanguage),
		"which regexes recognise the handout bracket: "+strings.Join(i18n.Languages(), ", "))
	separate := fs.Bool("separate", false, "a file per question instead of one for the package")
	listHandouts := fs.Bool("list_handouts", false, "also write which questions have a handout")
	config := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := applyConfig(fs, *config); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("handouts generate takes exactly one .4s file")
	}
	in := fs.Arg(0)
	src, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	dir := filepath.Dir(in)
	base := strings.TrimSuffix(filepath.Base(in), filepath.Ext(in))
	files, warnings, err := handout.Generate(fsource.Parse(string(src), gameOf(in)), base, dir,
		handout.GenerateOptions{Language: *language, Separate: *separate, ListHandouts: *listHandouts})
	if err != nil {
		return err
	}
	for _, w := range warnings {
		warn("%s", xystrings.Default.Chgkcli.Handouts.WarnQuestion(w.Number, w.Text))
	}
	for _, f := range files {
		out := filepath.Join(dir, f.Name)
		if err := os.WriteFile(out, []byte(f.Content), 0o644); err != nil {
			return err
		}
		reportOutput(out)
	}
	return nil
}

// handoutArgs declares the geometry flags `run` and `split_fit` share.
func handoutArgs(fs *flag.FlagSet) func() (handout.Args, string, error) {
	a := handout.DefaultArgs()
	typstBin := typstFlag(fs)
	language := fs.String("language", override("language", i18n.DefaultLanguage),
		"which labels the printed captions use: "+strings.Join(i18n.Languages(), ", "))
	labelsFile := fs.String("labels_file", "", "a labels TOML of your own, in place of the language's")
	font := fs.String("font", override("font", ""), "font family; empty is the bundled Noto Sans")
	fs.IntVar(&a.FontSize, "font_size", a.FontSize, "font size, pt")
	fs.IntVar(&a.PaperWidth, "paperwidth", a.PaperWidth, "paper width, mm")
	fs.IntVar(&a.PaperHeight, "paperheight", a.PaperHeight, "paper height, mm")
	fs.IntVar(&a.MarginTop, "margin_top", a.MarginTop, "top margin, mm")
	fs.IntVar(&a.MarginBottom, "margin_bottom", a.MarginBottom, "bottom margin, mm")
	fs.IntVar(&a.MarginLeft, "margin_left", a.MarginLeft, "left margin, mm")
	fs.IntVar(&a.MarginRight, "margin_right", a.MarginRight, "right margin, mm")
	boxWidth := fs.Float64("boxwidth", 0, "cell width, mm; 0 lets the layout choose")
	tikzMM := fs.Float64("tikz_mm", 0, "gap between cells, mm; 0 is the default 2")
	return func() (handout.Args, string, error) {
		if !i18n.Known(*language) {
			return a, "", fmt.Errorf("--language: %q is not one of %s", *language, strings.Join(i18n.Languages(), ", "))
		}
		a.Language, a.LabelsFile, a.Font = *language, *labelsFile, *font
		if *boxWidth != 0 {
			a.BoxWidth = boxWidth
		}
		if *tikzMM != 0 {
			a.TikzMM = tikzMM
		}
		return a, *typstBin, nil
	}
}

func handoutsRun(args []string) error {
	fs := flag.NewFlagSet("handouts run", flag.ContinueOnError)
	read := handoutArgs(fs)
	watch := fs.Bool("watch", false, "re-render whenever the file changes; Ctrl-C to stop")
	config := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := applyConfig(fs, *config); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("handouts run takes exactly one .hndt file")
	}
	a, typstBin, err := read()
	if err != nil {
		return err
	}
	in := fs.Arg(0)
	ts, closeTS, err := typesetter(typstBin)
	if err != nil {
		return err
	}
	defer closeTS()
	// chgksuite names it <stem>_<language>.pdf.
	out := outputName(in, "pdf", "_"+a.Language, false)
	render := func() error {
		hndt, images, err := readHandoutSource(in)
		if err != nil {
			return err
		}
		pdf, err := handout.Render(context.Background(), hndt, images, a, ts)
		if err != nil {
			return err
		}
		if err := os.WriteFile(out, pdf, 0o644); err != nil {
			return err
		}
		reportOutput(out)
		return nil
	}
	if err := render(); err != nil {
		return err
	}
	if !*watch {
		return nil
	}
	// chgksuite watches with watchdog; a second's poll on the file's own
	// timestamp is the same thing for one file, and needs nothing.
	reportNote("%s", xystrings.Default.Chgkcli.Handouts.WatchNote(in))
	last := modTime(in)
	for {
		time.Sleep(time.Second)
		if t := modTime(in); !t.Equal(last) {
			last = t
			if err := render(); err != nil {
				fail(err)
			}
		}
	}
}

func modTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// handoutsInstall is chgksuite's `handouts install`: fetch the tools the
// renderers need, into the directory it uses, so neither tool has to fetch them
// again. Every command does this on its own when it needs one; this is the way
// to do it before a plane.
func handoutsInstall(args []string) error {
	fs := flag.NewFlagSet("handouts install", flag.ContinueOnError)
	browser := fs.Bool("browser", false, "also fetch a chromium, which only html2img needs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	typst, err := typstinstall.FindOrInstall(context.Background(), func(s string) { reportNote("typst: %s", s) })
	if err != nil {
		return err
	}
	reportOutput(typst)
	if !*browser {
		return nil
	}
	chromium, err := htmlshot.FindOrInstall(context.Background(), "", func(s string) { reportNote("chromium: %s", s) })
	if err != nil {
		return err
	}
	reportOutput(chromium)
	return nil
}

// handoutsCreateHTML is create_html: the scaffold for a handout laid out by
// hand in a browser rather than by the .hndt format.
func handoutsCreateHTML(args []string) error {
	fs := flag.NewFlagSet("handouts create_html", flag.ContinueOnError)
	font := fs.String("font", "", "font family; empty is the browser's sans-serif")
	output := fs.String("output", "", "output filename; empty is handout_<fraction>.html")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("handouts create_html takes a fraction of the page: 1/6, 1/3, 1/2 or 1")
	}
	widths := map[string]float64{"1/6": 1.0 / 6, "1/3": 1.0 / 3, "1/2": 0.5, "1": 1}
	share, ok := widths[fs.Arg(0)]
	if !ok {
		return fmt.Errorf("%q is not one of 1/6, 1/3, 1/2, 1", fs.Arg(0))
	}
	// A4 less the 5 mm margins the renderer uses.
	widthMM := (210.0 - 2*5) * share
	name := *output
	if name == "" {
		name = "handout_" + strings.ReplaceAll(fs.Arg(0), "/", "_") + ".html"
	}
	if _, err := os.Stat(name); err == nil {
		return fmt.Errorf("%s", xystrings.Default.Chgkcli.Handouts.AlreadyExists(name))
	}
	family := *font
	if family == "" {
		family = "sans-serif"
	}
	html := fmt.Sprintf(htmlHandoutTemplate, strconv.FormatFloat(widthMM, 'f', 1, 64), family)
	if err := os.WriteFile(name, []byte(html), 0o644); err != nil {
		return err
	}
	reportOutput(name)
	reportNote("%s", xystrings.Default.Chgkcli.Handouts.HtmlGeometryNote(strconv.FormatFloat(widthMM, 'f', 1, 64), fs.Arg(0)))
	return nil
}

// handoutsHTML2Img is html2img: the hand-laid-out HTML into the PDF that goes
// to the printer and the PNG that goes into a chat.
func handoutsHTML2Img(args []string) error {
	fs := flag.NewFlagSet("handouts html2img", flag.ContinueOnError)
	scale := fs.Float64("scale", 2, "the PNG's device pixel ratio")
	browser := fs.String("browser", override("browser", ""),
		"the chromium to render with; empty looks for one (also $CHGKSUITE_BROWSER)")
	config := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := applyConfig(fs, *config); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("handouts html2img takes exactly one .html file")
	}
	res, err := htmlshot.Render(context.Background(), fs.Arg(0), htmlshot.Options{
		Browser: *browser, Scale: *scale,
		Say: func(s string) { warn("chromium: %s", s) },
	})
	if err != nil {
		return err
	}
	reportOutput(res.PDF)
	reportNote("%s", xystrings.Default.Chgkcli.Handouts.DimensionsNote(strconv.FormatFloat(res.WidthMM, 'f', 1, 64), strconv.FormatFloat(res.HeightMM, 'f', 1, 64)))
	reportOutput(res.PNG)
	reportNote("%s", xystrings.Default.Chgkcli.Handouts.ScaleNote(strconv.FormatFloat(*scale, 'g', -1, 64)))
	return nil
}

const htmlHandoutTemplate = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
  * {
    margin: 0;
    padding: 0;
    box-sizing: border-box;
  }
  html, body {
    width: %smm;
  }
  body {
    font-family: %s;
    font-size: 14pt;
    padding: 2mm;
  }
</style>
</head>
<body>

<p>Edit this file</p>

</body>
</html>
`

func handoutsSplitFit(args []string) error {
	fs := flag.NewFlagSet("handouts split_fit", flag.ContinueOnError)
	read := handoutArgs(fs)
	outputDir := fs.String("output_dir", "", "where to write the zip; empty is beside the input")
	config := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := applyConfig(fs, *config); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("handouts split_fit takes exactly one .hndt file")
	}
	a, typstBin, err := read()
	if err != nil {
		return err
	}
	in := fs.Arg(0)
	hndt, images, err := readHandoutSource(in)
	if err != nil {
		return err
	}
	ts, closeTS, err := typesetter(typstBin)
	if err != nil {
		return err
	}
	defer closeTS()
	zipped, err := handout.SplitFit(context.Background(), hndt, images, a, ts)
	if err != nil {
		return err
	}
	dir := *outputDir
	if dir == "" {
		dir = filepath.Dir(in)
	}
	out := filepath.Join(dir, strings.TrimSuffix(filepath.Base(in), filepath.Ext(in))+"_split_fit.zip")
	if err := os.WriteFile(out, zipped, 0o644); err != nil {
		return err
	}
	reportOutput(out)
	return nil
}

// readHandoutSource reads a .hndt and the pictures its blocks name, which sit
// beside it.
func readHandoutSource(in string) (string, map[string][]byte, error) {
	raw, err := os.ReadFile(in)
	if err != nil {
		return "", nil, err
	}
	hndt := string(raw)
	images := map[string][]byte{}
	dir := filepath.Dir(in)
	for _, name := range handout.ImageNames(hndt) {
		// generate writes an absolute path, a hand-written .hndt usually a bare
		// name; either is read as the key typst will ask for.
		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", nil, err
		}
		images[name] = data
	}
	return hndt, images, nil
}

// handoutsPack is pack.py: every split-fitted single-handout .hndt in a folder,
// each repeated for as many pages as the field needs, merged into one PDF for
// the colour printer and one for the black-and-white one.
func handoutsPack(args []string) error {
	fs := flag.NewFlagSet("handouts pack", flag.ContinueOnError)
	prefix := fs.String("output_filename_prefix", "packed_handouts", "output filename prefix")
	nTeams := fs.Int("n_teams", 0, "number of teams (required)")
	read := handoutArgs(fs)
	compress := fs.String("compress_pdf", "on", "compress the merged PDF: on|off")
	config := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := applyConfig(fs, *config); err != nil {
		return err
	}
	if *nTeams <= 0 {
		return fmt.Errorf("handouts pack needs --n_teams")
	}
	a, typstBin, err := read()
	if err != nil {
		return err
	}
	folder := "."
	if fs.NArg() > 0 {
		folder = fs.Arg(0)
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && (strings.HasSuffix(e.Name(), ".hndt") || strings.HasSuffix(e.Name(), ".txt")) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	ts, closeTS, err := typesetter(typstBin)
	if err != nil {
		return err
	}
	defer closeTS()

	var colour, bw [][]byte
	for _, name := range names {
		path := filepath.Join(folder, name)
		hndt, images, err := readHandoutSource(path)
		if err != nil {
			warn("%s", xystrings.Default.Chgkcli.Handouts.SkipWarning(name, err.Error()))
			continue
		}
		pages, isColour, err := handout.PackPages(hndt, *nTeams)
		if err != nil {
			warn("%s", xystrings.Default.Chgkcli.Handouts.SkipWarning(name, err.Error()))
			continue
		}
		pdf, err := handout.Render(context.Background(), hndt, images, a, ts)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		reportNote("%s", xystrings.Default.Chgkcli.Handouts.PagesNote(name, strconv.Itoa(pages), colourNote(isColour)))
		for range pages {
			if isColour {
				colour = append(colour, pdf)
			} else {
				bw = append(bw, pdf)
			}
		}
	}
	for _, out := range []struct {
		suffix string
		pages  [][]byte
	}{{"_color.pdf", colour}, {"_bw.pdf", bw}} {
		if len(out.pages) == 0 {
			continue
		}
		merged, err := handout.MergePDFs(out.pages, *compress == "on")
		if err != nil {
			return err
		}
		path := filepath.Join(folder, *prefix+out.suffix)
		if err := os.WriteFile(path, merged, 0o644); err != nil {
			return err
		}
		reportOutput(path)
	}
	return nil
}

func colourNote(colour bool) string {
	if colour {
		return xystrings.Default.Chgkcli.Handouts.ColourSuffix()
	}
	return ""
}
