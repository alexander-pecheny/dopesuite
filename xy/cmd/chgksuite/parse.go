package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"xy/internal/chgk/docxread"
	"xy/internal/chgk/fsource"
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
		// Only a чгк .txt is escaped on the way in: chgksuite calls
		// escape_underscores_except_urls in chgk_parse_txt and nowhere else, so a
		// .docx (whose italics are already markers) and a СИ .txt keep theirs.
		if game == "chgk" || game == "brain" {
			text = typo.EscapeUnderscoresExceptURLs(text, false)
		}
		return text, nil, err
	default:
		return "", nil, fmt.Errorf("unsupported file format")
	}
}

func parseText(text, game, in string, a parseArgs) (fsource.Doc, error) {
	switch game {
	case "si":
		return textparse.ParseSI(text), nil
	case "troika":
		return textparse.ParseTroika(text), nil
	case "chgk", "brain":
		return textparse.Parse(text, textparse.Options{
			SingleNumberLines:  a.singleNumberLines,
			DefaultAuthor:      defaultAuthorFor(a.defaultAuthor, in),
			TourNumbersAsWords: a.tourNumbersAsWords,
		}), nil
	default:
		return nil, fmt.Errorf("unknown game %q", game)
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
