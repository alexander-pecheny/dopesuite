package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"xy/internal/chgk/docxread"
	"xy/internal/chgk/fsource"
	"xy/internal/chgk/i18n"
	"xy/internal/chgk/textenc"
	"xy/internal/chgk/textparse"
	"xy/internal/chgk/typo"
)

// parseCmd is `chgksuite parse`: a .docx or .txt package into the 4s file the
// rest of the tool reads.
func parseCmd(args []string) error {
	fs := flag.NewFlagSet("parse", flag.ContinueOnError)
	game := fs.String("game", "", "chgk (default), brain, si or troika")
	encoding := fs.String("encoding", "", "encoding of a .txt file ("+strings.Join(textenc.Encodings(), ", ")+"); guessed when empty")
	defaultAuthor := fs.String("defaultauthor", "off", `credit questions with no author: "off", "file" (the file's name) or a name`)
	numbers := fs.String("numbers_handling", "default", "question numbers written out: default, all or none")
	preserveFormatting := fs.Bool("preserve_formatting", false, "keep bold and italic")
	singleNumberLines := fs.String("single_number_line_handling", "smart", "a line that is only a number: smart, on or off")
	tourNumbersAsWords := fs.String("tour_numbers_as_words", "off", "rename the tours «Первый тур», «Второй тур»…: on|off")
	linksOld := fs.String("links", "unwrap", "hyperlinks: unwrap (text, then the URL) or old (the URL alone)")
	noImagePrefix := fs.Bool("no_image_prefix", false, "name extracted images without the file's name in front")
	addTS := fs.String("add_ts", "off", "append a timestamp to the output filename: on|off")
	language := fs.String("language", i18n.DefaultLanguage, "which labels and field markers to read the package by: "+strings.Join(i18n.Languages(), ", "))
	quotes := fs.String("typography_quotes", "on", "quotes: on, off or smart (a package that already types « » is left alone)")
	accents := fs.String("typography_accents", "on", "stress marks: on, off, light (homoglyphs only) or smart")
	dashes := fs.String("typography_dashes", "on", "dashes: on|off")
	whitespace := fs.String("typography_whitespace", "on", "trim and collapse whitespace: on|off")
	percent := fs.String("typography_percent", "on", "decode %-escapes; chgksuite reads this switch and decodes either way")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("no input file")
	}
	for _, c := range []struct {
		name, value string
		allowed     []string
	}{
		{"game", *game, []string{"", "chgk", "brain", "si", "troika"}},
		{"numbers_handling", *numbers, []string{"default", "all", "none"}},
		{"single_number_line_handling", *singleNumberLines, []string{"smart", "on", "off"}},
		{"tour_numbers_as_words", *tourNumbersAsWords, []string{"on", "off"}},
		{"links", *linksOld, []string{"unwrap", "old"}},
		{"add_ts", *addTS, []string{"on", "off"}},
		{"typography_quotes", *quotes, []string{"on", "off", "smart"}},
		{"typography_accents", *accents, []string{"on", "off", "light", "smart"}},
		{"typography_dashes", *dashes, []string{"on", "off"}},
		{"typography_whitespace", *whitespace, []string{"on", "off"}},
		{"typography_percent", *percent, []string{"on", "off"}},
		{"language", *language, i18n.Languages()},
	} {
		if !slices.Contains(c.allowed, c.value) {
			return fmt.Errorf("--%s: %q is not one of %s", c.name, c.value, strings.Join(c.allowed, ", "))
		}
	}
	for _, in := range fs.Args() {
		out, err := parseFile(in, parseArgs{
			game:               *game,
			encoding:           *encoding,
			defaultAuthor:      *defaultAuthor,
			numbers:            *numbers,
			preserveFormatting: *preserveFormatting,
			singleNumberLines:  *singleNumberLines,
			tourNumbersAsWords: *tourNumbersAsWords == "on",
			linksOld:           *linksOld == "old",
			noImagePrefix:      *noImagePrefix,
			addTS:              *addTS == "on",
			language:           *language,
			typo: typo.Options{
				Whitespace: *whitespace == "on",
				Quotes:     typo.Mode(*quotes),
				Dashes:     *dashes == "on",
				Accents:    typo.Mode(*accents),
				// chgksuite tests the switch for truth, not for "on", so it
				// decodes whatever the flag says. Same here.
				Percent: true,
			},
		})
		if err != nil {
			return fmt.Errorf("%s: %w", in, err)
		}
		fmt.Println("Output:", out)
	}
	return nil
}

type parseArgs struct {
	game, encoding, defaultAuthor, numbers, singleNumberLines string
	preserveFormatting, tourNumbersAsWords, linksOld          bool
	noImagePrefix, addTS                                      bool
	language                                                  string
	typo                                                      typo.Options
}

// parseFile reads one package and writes its 4s beside it, returning the name.
func parseFile(in string, a parseArgs) (string, error) {
	game := a.game
	if game == "" {
		game = "chgk"
	}
	// СИ numbers are point values and a троика's repeat in every theme, so both
	// write every number out unless asked for something else.
	numbers := fsource.NumbersHandling(a.numbers)
	if (game == "si" || game == "troika") && numbers == fsource.NumbersDefault {
		numbers = fsource.NumbersAll
	}

	text, images, err := readSource(in, game, a)
	if err != nil {
		return "", err
	}
	doc, err := parseText(text, game, in, a)
	if err != nil {
		return "", err
	}
	for _, img := range images {
		if err := os.WriteFile(filepath.Join(filepath.Dir(in), img.Name), img.Data, 0o644); err != nil {
			return "", err
		}
	}
	out := outputName(in, gameExt(game), "", a.addTS)
	return out, os.WriteFile(out, []byte(fsource.Compose(doc, numbers)), 0o644)
}

// readSource turns the input file into the plain text a parser reads, plus the
// pictures a .docx carried.
func readSource(in, game string, a parseArgs) (string, []docxread.Image, error) {
	raw, err := os.ReadFile(in)
	if err != nil {
		return "", nil, err
	}
	switch strings.ToLower(filepath.Ext(in)) {
	case ".docx":
		base := strings.TrimSuffix(filepath.Base(in), filepath.Ext(in))
		prefix := strings.ReplaceAll(base, " ", "_") + "_"
		if a.noImagePrefix {
			prefix = ""
		}
		text, images, err := docxread.ToText(raw, docxread.Options{
			PreserveFormatting: a.preserveFormatting,
			LinksOld:           a.linksOld,
			ImagePrefix:        prefix,
			// The СИ and троика parsers read the document's own outline, and
			// chgksuite lets their numbered lists start where they say.
			HeadingMarkers:    game == "si" || game == "troika",
			PreserveListStart: game == "si" || game == "troika",
		})
		return text, images, err
	case ".txt":
		text, err := textenc.Decode(raw, a.encoding)
		return text, nil, err
	default:
		return "", nil, fmt.Errorf("unsupported file format")
	}
}

func parseText(text, game, in string, a parseArgs) (fsource.Doc, error) {
	switch game {
	case "si":
		return textparse.ParseSI(text, a.typo, a.language), nil
	case "troika":
		return textparse.ParseTroika(text, a.typo, a.language), nil
	case "chgk", "brain":
		if strings.EqualFold(filepath.Ext(in), ".txt") {
			// chgk_parse_txt, in its order: db.chgk.info's own export is read as
			// itself, and only a package written by hand gets the escaping.
			if textparse.IsDBExport(text) {
				return textparse.ParseDB(text, dbFetcher(filepath.Dir(in))), nil
			}
			text = typo.EscapeUnderscoresExceptURLs(text, false)
		}
		return textparse.Parse(text, textparse.Options{
			SingleNumberLines:  a.singleNumberLines,
			DefaultAuthor:      defaultAuthorFor(a.defaultAuthor, in),
			TourNumbersAsWords: a.tourNumbersAsWords,
			Typo:               a.typo,
			Language:           a.language,
		}), nil
	default:
		return nil, fmt.Errorf("unknown game %q", game)
	}
}

// dbFetcher downloads a picture or sound a db.chgk.info export names, skipping
// what is already on disk. chgksuite fetches into the working directory; this
// puts the file beside the one being parsed, where the .4s coming out of it
// looks for its pictures.
func dbFetcher(dir string) textparse.Fetcher {
	client := &http.Client{Timeout: 30 * time.Second}
	return func(url, name string) error {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		resp, err := client.Get(url)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s: %s", url, resp.Status)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o644)
	}
}

// defaultAuthorFor reads --defaultauthor: "off" is nobody, "file" is the file's
// own name, anything else is itself.
func defaultAuthorFor(setting, in string) string {
	switch setting {
	case "", "off":
		return ""
	case "file":
		return strings.TrimSuffix(filepath.Base(in), filepath.Ext(in))
	default:
		return setting
	}
}

// gameExt is game_to_ext: each game writes its own extension.
func gameExt(game string) string {
	switch game {
	case "si":
		return "si4s"
	case "brain":
		return "br4s"
	case "troika":
		return "tr4s"
	default:
		return "4s"
	}
}
