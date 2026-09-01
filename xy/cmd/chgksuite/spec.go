package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"xy/internal/chgk/i18n"
)

// `chgksuite spec` prints, as JSON, every command this tool has: what it takes,
// and every flag it declares with that flag's help, default and choices. It is
// how chgksuite-gui draws its form, so a flag added here appears there without
// anyone editing the GUI. It is not in the usage table because it is for that
// program to read rather than for a person.
//
// Collecting the flags works because every command declares them and then calls
// parseFlags. With a collector armed, that hands the flag set over and returns
// errFlagsCollected, which travels out through the command's own "return err"
// before it has done anything.

// flagSet is a flag.FlagSet that remembers the order its flags were declared
// in. The GUI draws a form in that order, which is the order the command's
// author put them in; flag.VisitAll hands them over alphabetically, which would
// open every form with --add_ts.
type flagSet struct {
	*flag.FlagSet
	order []string
}

func newFlagSet(name string) *flagSet {
	return &flagSet{FlagSet: flag.NewFlagSet(name, flag.ContinueOnError)}
}

func (f *flagSet) String(name, value, usage string) *string {
	f.order = append(f.order, name)
	return f.FlagSet.String(name, value, usage)
}

func (f *flagSet) Bool(name string, value bool, usage string) *bool {
	f.order = append(f.order, name)
	return f.FlagSet.Bool(name, value, usage)
}

func (f *flagSet) Int(name string, value int, usage string) *int {
	f.order = append(f.order, name)
	return f.FlagSet.Int(name, value, usage)
}

func (f *flagSet) IntVar(p *int, name string, value int, usage string) {
	f.order = append(f.order, name)
	f.FlagSet.IntVar(p, name, value, usage)
}

func (f *flagSet) Float64(name string, value float64, usage string) *float64 {
	f.order = append(f.order, name)
	return f.FlagSet.Float64(name, value, usage)
}

var collector func(*flagSet)

var errFlagsCollected = errors.New("flags collected")

func parseFlags(fs *flagSet, args []string) error {
	if collector != nil {
		collector(fs)
		return errFlagsCollected
	}
	return fs.Parse(args)
}

type flagSpec struct {
	Name     string   `json:"name"`
	Usage    string   `json:"usage"`
	Default  string   `json:"default"`
	Bool     bool     `json:"bool,omitempty"`
	Advanced bool     `json:"advanced,omitempty"`
	Choices  []string `json:"choices,omitempty"`
	Path     string   `json:"path,omitempty"` // "file" or "folder", for the ones that name one
}

// input is a positional argument, or the run of them a command ends with.
type input struct {
	Kind     string   `json:"kind"` // file, files, folder, text or choice
	Label    string   `json:"label"`
	Ext      []string `json:"ext,omitempty"`
	Choices  []string `json:"choices,omitempty"`
	Optional bool     `json:"optional,omitempty"`
}

type commandSpec struct {
	Verb   string     `json:"verb"`
	What   string     `json:"what"`
	Inputs []input    `json:"inputs,omitempty"`
	Flags  []flagSpec `json:"flags"`
}

const (
	kindFile   = "file"
	kindFiles  = "files"
	kindFolder = "folder"
	kindText   = "text"
	kindChoice = "choice"
)

var (
	packetExt  = []string{".4s", ".si4s", ".br4s", ".tr4s"}
	packets    = []input{{Kind: kindFiles, Label: "packets", Ext: packetExt}}
	onePacket  = []input{{Kind: kindFile, Label: "packet", Ext: packetExt}}
	oneHandout = []input{{Kind: kindFile, Label: "handout file", Ext: []string{".hndt"}}}
)

// runnables is every command that can be run, with what its positional
// arguments are. What each one does comes from the usage table, so the two
// cannot disagree; spec_test.go checks that they list the same commands.
var runnables = []struct {
	verb   string
	inputs []input
	run    func([]string) error
}{
	{"parse", []input{{Kind: kindFiles, Label: "packets", Ext: []string{".docx", ".txt"}}}, parseCmd},
	{"compose docx", packets, composeDocx},
	{"compose pdf", packets, composePDF},
	{"compose pptx", packets, composePptx},
	{"compose telegram", onePacket, composeTelegram},
	{"compose markdown", packets, publishedAs("markdown")},
	{"compose redditmd", packets, publishedAs("redditmd")},
	{"compose base", packets, publishedAs("base")},
	{"compose openquiz", packets, publishedAs("openquiz")},
	{"compose lj", packets, composeLJ},
	{"compose add_stats", packets, composeAddStats},
	{"handouts generate", onePacket, handoutsGenerate},
	{"handouts run", oneHandout, handoutsRun},
	{"handouts split_fit", oneHandout, handoutsSplitFit},
	{"handouts pack", []input{{Kind: kindFolder, Label: "folder of split-fitted PDFs", Optional: true}}, handoutsPack},
	{"handouts create_html", []input{{Kind: kindChoice, Label: "share of an A4 sheet", Choices: []string{"1/6", "1/3", "1/2", "1"}}}, handoutsCreateHTML},
	{"handouts html2img", []input{{Kind: kindFile, Label: "HTML file", Ext: []string{".html"}}}, handoutsHTML2Img},
	{"handouts install", nil, handoutsInstall},
	{"board token", []input{{Kind: kindText, Label: "service URL; empty is trello", Optional: true}}, boardToken},
	{"board download", []input{{Kind: kindFolder, Label: "folder to download into"}}, boardDownload},
	{"board upload", []input{
		{Kind: kindText, Label: "board URL"},
		{Kind: kindFiles, Label: "packets to upload", Ext: packetExt},
	}, boardUpload},
}

func publishedAs(filetype string) func([]string) error {
	return func(args []string) error { return composePublished(filetype, args) }
}

// choices are the values a flag takes. Most flags write them into their own
// help as "on|off", which choicesOf reads; these are the ones whose help reads
// as a sentence instead.
var choices = map[string][]string{
	"game":                        {"chgk", "brain", "si", "troika"},
	"numbers_handling":            {"default", "all", "none"},
	"single_number_line_handling": {"smart", "on", "off"},
	"links":                       {"unwrap", "old"},
	"typography_quotes":           {"on", "off", "smart"},
	"typography_accents":          {"on", "off", "light", "smart"},
	"typography_percent":          {"on", "off"},
	"device":                      {"desktop", "mobile"},
	"language":                    i18n.Languages(),
}

// paths are the flags that name a file or a folder, and so get a picker.
var paths = map[string]string{
	"config":        kindFile,
	"docx_template": kindFile,
	"template":      kindFile,
	"labels_file":   kindFile,
	"pdf_config":    kindFile,
	"pptx_config":   kindFile,
	"poll_config":   kindFile,
	"custom_csv":    kindFile,
	"typst":         kindFile,
	"browser":       kindFile,
	"output_dir":    kindFolder,
	"font_dir":      kindFolder,
}

// advanced is the flags the GUI folds away, which is the division chgksuite's
// own window makes (its cli.py marks them advanced=True). The ones this port
// added are placed by the same rule: a flag that says where a binary or a
// directory is, or that trims a millimetre off a layout, is not what someone
// exporting a packet came to set.
var advanced = map[string]bool{
	"add_polls":                   true,
	"add_ts":                      true,
	"boxwidth":                    true,
	"browser":                     true,
	"compress_pdf":                true,
	"config":                      true,
	"custom_csv_args":             true,
	"defaultauthor":               true,
	"do_not_remove_accents":       true,
	"docx_template":               true,
	"dry_run":                     true,
	"encoding":                    true,
	"font_dir":                    true,
	"imgur_client_id":             true,
	"labels_file":                 true,
	"links":                       true,
	"margin_bottom":               true,
	"margin_left":                 true,
	"margin_right":                true,
	"margin_top":                  true,
	"merge":                       true,
	"no_image_prefix":             true,
	"noparagraph":                 true,
	"numbers_handling":            true,
	"only_question_number":        true,
	"optimize_size":               true,
	"paperheight":                 true,
	"paperwidth":                  true,
	"pdf_config":                  true,
	"poll_config":                 true,
	"pptx_config":                 true,
	"preserve_formatting":         true,
	"randomize":                   true,
	"rawtypst":                    true,
	"replace_no_break_hyphens":    true,
	"replace_no_break_spaces":     true,
	"single_number_line_handling": true,
	"smaller_source_and_author":   true,
	"stop_if_no_stats":            true,
	"template":                    true,
	"tikz_mm":                     true,
	"tour_numbers_as_words":       true,
	"typography_accents":          true,
	"typography_dashes":           true,
	"typography_percent":          true,
	"typography_quotes":           true,
	"typography_whitespace":       true,
	"typst":                       true,
	"watch":                       true,
}

// advancedHere is where that division depends on the command, as it does in
// chgksuite: the language matters when a packet is read or exported and not
// when a handout is drawn, and a font is the whole look of a handout but
// something a .docx or .pptx template already carries.
var advancedHere = map[string]map[string]bool{
	"compose docx":       {"font": true},
	"compose pptx":       {"font": true},
	"board download":     {"font": true},
	"handouts generate":  {"language": true},
	"handouts run":       {"language": true},
	"handouts split_fit": {"language": true},
	"handouts pack":      {"language": true},
}

func isAdvanced(verb, name string) bool {
	if v, ok := advancedHere[verb][name]; ok {
		return v
	}
	return advanced[name]
}

var choiceList = regexp.MustCompile(`^[a-z0-9_]+(\|[a-z0-9_]+)+$`)

// choicesOf reads the values a flag takes off the end of its own help, where a
// flag with a handful of them writes them: "hide answers: off|whiten|dots".
func choicesOf(f *flag.Flag) []string {
	if c, ok := choices[f.Name]; ok {
		return c
	}
	i := strings.LastIndex(f.Usage, ": ")
	if i < 0 {
		return nil
	}
	if tail := f.Usage[i+2:]; choiceList.MatchString(tail) {
		return strings.Split(tail, "|")
	}
	return nil
}

// flagsOf runs a command far enough to declare its flags and no further.
func flagsOf(verb string, run func([]string) error) ([]flagSpec, error) {
	var set *flagSet
	collector = func(fs *flagSet) { set = fs }
	defer func() { collector = nil }()
	if err := run(nil); !errors.Is(err, errFlagsCollected) {
		return nil, fmt.Errorf("stopped at %v rather than at its flags", err)
	}
	out := make([]flagSpec, 0, len(set.order))
	for _, name := range set.order {
		f := set.Lookup(name)
		b, ok := f.Value.(interface{ IsBoolFlag() bool })
		out = append(out, flagSpec{
			Name:     f.Name,
			Usage:    f.Usage,
			Default:  f.DefValue,
			Bool:     ok && b.IsBoolFlag(),
			Choices:  choicesOf(f),
			Path:     paths[f.Name],
			Advanced: isAdvanced(verb, f.Name),
		})
	}
	return out, nil
}

// whatOf is the command's line in the usage table, where one row can stand for
// several commands: "compose markdown|redditmd" describes both of them.
func whatOf(verb string) string {
	for _, c := range commands {
		for _, v := range expandVerb(c.verb) {
			if v == verb {
				return c.what
			}
		}
	}
	return ""
}

func expandVerb(verb string) []string {
	head, tail, ok := strings.Cut(verb, " ")
	if !ok {
		return []string{verb}
	}
	var out []string
	for _, alt := range strings.Split(tail, "|") {
		out = append(out, head+" "+alt)
	}
	return out
}

func specCmd() error {
	out := make([]commandSpec, 0, len(runnables))
	for _, r := range runnables {
		flags, err := flagsOf(r.verb, r.run)
		if err != nil {
			return fmt.Errorf("%s: %w", r.verb, err)
		}
		out = append(out, commandSpec{Verb: r.verb, What: whatOf(r.verb), Inputs: r.inputs, Flags: flags})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
