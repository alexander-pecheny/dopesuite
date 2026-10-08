package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	xystrings "xy/i18nstrings"
	"xy/internal/chgk/fsource"
	"xy/internal/chgk/stats"
)

// composeAddStats is `chgksuite compose add_stats`: read a tournament's results
// and write a copy of the packet with «Взятия: N/M» on every question.
func composeAddStats(args []string) error {
	fs := newFlagSet("compose add_stats")
	ratingIDs := fs.String("rating_ids", "", "rating.chgk.info tournament id, comma-separated for sync+async")
	customCSV := fs.String("custom_csv", "", xystrings.Default.Chgkcli.AddStats.CustomCsvFlag())
	csvArgs := fs.String("custom_csv_args", "{}", `csv reader options as JSON, e.g. {"delimiter": ";"}`)
	questionRange := fs.String("question_range", "", `range of question numbers to include, e.g. "25-36"`)
	threshold := fs.Int("team_naming_threshold", overrideInt("team_naming_threshold", 2), "name the teams when this few took the question")
	addTS := fs.String("add_ts", override("add_ts", "off"), "append a timestamp to the output filename: on|off")
	merge := fs.Bool("merge", false, "read the input files as one packet")
	config := configFlag(fs)
	if err := parseConfigured(fs, args, *config); err != nil {
		return err
	}
	if (*ratingIDs == "") == (*customCSV == "") {
		return fmt.Errorf("add_stats needs either --rating_ids or --custom_csv")
	}
	delimiter, err := csvDelimiter(*csvArgs)
	if err != nil {
		return err
	}

	var results []stats.Result
	if *ratingIDs != "" {
		if results, err = stats.Fetch(context.Background(), *ratingIDs); err != nil {
			return err
		}
	} else {
		for _, name := range splitFiles(*customCSV) {
			r, warnings, err := stats.ReadFile(name, delimiter)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			for _, w := range warnings {
				warn("%s: %s", name, w)
			}
			results = append(results, r...)
		}
	}

	opts := stats.DefaultOptions()
	opts.QuestionRange = *questionRange
	opts.TeamNamingThreshold = *threshold
	sources, err := loadSources(fs.Args(), *merge)
	if err != nil {
		return err
	}
	for _, s := range sources {
		if err := stats.Add(s.doc, results, opts); err != nil {
			return err
		}
		out := outputName(s.path, "4s", "_with_stats", *addTS == "on")
		if err := os.WriteFile(out, []byte(fsource.Compose(s.doc, fsource.NumbersDefault)), outputFileMode); err != nil {
			return err
		}
		reportOutput(out)
	}
	return nil
}

// splitFiles reads --custom_csv, which takes several files comma-separated —
// unless a comma is part of a name that exists, as chgksuite's own check allows.
func splitFiles(s string) []string {
	parts := strings.Split(s, ",")
	if len(parts) == 1 {
		return parts
	}
	for _, p := range parts {
		if _, err := os.Stat(p); err != nil {
			return []string{s}
		}
	}
	return parts
}

func csvDelimiter(jsonArgs string) (rune, error) {
	var opts struct {
		Delimiter string `json:"delimiter"`
	}
	if err := json.Unmarshal([]byte(jsonArgs), &opts); err != nil {
		return 0, fmt.Errorf("--custom_csv_args: %w", err)
	}
	if opts.Delimiter == "" {
		return ',', nil
	}
	d := []rune(opts.Delimiter)
	if len(d) != 1 {
		return 0, fmt.Errorf("--custom_csv_args: delimiter %q is not one character", opts.Delimiter)
	}
	return d[0], nil
}

// composeHasStats is `chgksuite compose has_stats <file>`: whether a packet
// already carries a stats line, as {"has_stats": true|false}. It is the check
// the spec's confirm names for compose telegram, which chgksuite-gui runs
// before a telegram export; it is for that program, so it is not in the usage
// table.
func composeHasStats(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("compose has_stats takes exactly one packet")
	}
	out, err := hasStatsJSON(args[0])
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(out)
	return err
}

func hasStatsJSON(path string) ([]byte, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Parsed as chgk, as composeTelegram parses it: this answers for the
	// telegram export, which takes chgk packets only.
	doc := fsource.Parse(string(src), "chgk")
	out, err := json.Marshal(map[string]bool{"has_stats": stats.HasStats(doc)})
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
