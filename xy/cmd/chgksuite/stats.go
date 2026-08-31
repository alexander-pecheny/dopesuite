package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/stats"
)

// composeAddStats is `chgksuite compose add_stats`: read a tournament's results
// and write a copy of the package with «Взятия: N/M» on every question.
func composeAddStats(args []string) error {
	fs := flag.NewFlagSet("compose add_stats", flag.ContinueOnError)
	ratingIDs := fs.String("rating_ids", "", "rating.chgk.info tournament id, comma-separated for sync+async")
	customCSV := fs.String("custom_csv", "", "a csv/xlsx результаты table in rating.chgk.info's format, comma-separated")
	csvArgs := fs.String("custom_csv_args", "{}", `csv reader options as JSON, e.g. {"delimiter": ";"}`)
	questionRange := fs.String("question_range", "", `range of question numbers to include, e.g. "25-36"`)
	threshold := fs.Int("team_naming_threshold", overrideInt("team_naming_threshold", 2), "name the teams when this few took the question")
	addTS := fs.String("add_ts", override("add_ts", "off"), "append a timestamp to the output filename: on|off")
	merge := fs.Bool("merge", false, "read the input files as one package")
	config := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := applyConfig(fs, *config); err != nil {
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
				fmt.Fprintf(os.Stderr, "%s: %s\n", name, w)
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
		if err := os.WriteFile(out, []byte(fsource.Compose(s.doc, fsource.NumbersDefault)), 0o644); err != nil {
			return err
		}
		fmt.Println("Output:", out)
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
