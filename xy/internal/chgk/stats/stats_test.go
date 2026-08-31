package stats

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xy/internal/chgk/fsource"
)

// The fixtures are chgksuite's own: two real вопросные таблицы of tournament
// 6290 in both layouts the rating site exports, a third real per-tour csv, and
// the .4s the oracles are built from. gen_stats_oracle.py writes the oracles by
// running chgksuite's own StatsAdder.

func TestReadTables(t *testing.T) {
	cases := []struct {
		file  string
		teams int
		team  string
		mask  string
	}{
		{"stats_tour.xlsx", 59, "09/13", "010001000000000000000000000000000000"},
		{"stats_full.xlsx", 59, "09/13", "010001000000000000000000000000000000"},
		{"stats_tour.csv", 33, "Acquired Taste", "001110110001100111100101111111110100"},
		{"stats_tour2.csv", 80, "Be Humble", "101100111110010100100100100101110101"},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			results, warnings, err := ReadFile(filepath.Join("testdata", c.file), ',')
			if err != nil {
				t.Fatal(err)
			}
			if len(warnings) != 0 {
				t.Errorf("warnings: %v", warnings)
			}
			if len(results) != c.teams {
				t.Errorf("teams = %d, want %d", len(results), c.teams)
			}
			found := false
			for _, r := range results {
				if len(r.Mask) != len(c.mask) {
					t.Fatalf("%s: mask %q", r.Name, r.Mask)
				}
				if r.Name == c.team {
					found = true
					if r.Mask != c.mask {
						t.Errorf("%s = %q, want %q", c.team, r.Mask, c.mask)
					}
				}
			}
			if !found {
				t.Errorf("no team %q", c.team)
			}
		})
	}
}

func TestTourAndFullAgree(t *testing.T) {
	tour, _, err := ReadXLSX("testdata/stats_tour.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	full, _, err := ReadXLSX("testdata/stats_full.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]string{}
	for _, r := range tour {
		byName[r.Name] = r.Mask
	}
	for _, r := range full {
		if byName[r.Name] != r.Mask {
			t.Errorf("%s: tour %q, full %q", r.Name, byName[r.Name], r.Mask)
		}
		delete(byName, r.Name)
	}
	if len(byName) != 0 {
		t.Errorf("only in the per-tour export: %v", byName)
	}
}

func TestDisputedValueIsNotTaken(t *testing.T) {
	rows := [][]string{
		{"Team ID", "Название", "Город", "1", "2", "3"},
		{"1", "Alpha", "Town", "1", "X", "1"},
	}
	results, warnings := ReadTable(rows)
	if len(results) != 1 || results[0].Mask != "101" {
		t.Fatalf("results = %+v", results)
	}
	if len(warnings) != 1 {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestHeaderlessTableWarns(t *testing.T) {
	results, warnings := ReadTable([][]string{{"1", "Alpha", "Town", "1", "0"}})
	if len(results) != 0 || len(warnings) != 1 {
		t.Errorf("results = %+v, warnings = %v", results, warnings)
	}
}

func TestParity(t *testing.T) {
	src, err := os.ReadFile("testdata/package.4s")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		file string
		opts Options
	}{
		{"tour_csv", "stats_tour.csv", DefaultOptions()},
		{"tour2_csv", "stats_tour2.csv", DefaultOptions()},
		{"tour_xlsx", "stats_tour.xlsx", DefaultOptions()},
		{"full_xlsx", "stats_full.xlsx", DefaultOptions()},
		{"range", "stats_tour.csv", Options{Label: "Взятия", QuestionRange: "13-24", TeamNamingThreshold: 2}},
		{"threshold", "stats_tour.csv", Options{Label: "Взятия", TeamNamingThreshold: 8}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			results, _, err := ReadFile(filepath.Join("testdata", c.file), ',')
			if err != nil {
				t.Fatal(err)
			}
			doc := fsource.Parse(string(src), "chgk")
			if err := Add(doc, results, c.opts); err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join("testdata", c.name+".4s.oracle"))
			if err != nil {
				t.Fatal(err)
			}
			if got := fsource.Compose(doc, fsource.NumbersDefault); got != string(want) {
				t.Errorf("differs from chgksuite:\n%s", firstDiff(got, string(want)))
			}
		})
	}
}

func firstDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	at := func(lines []string, i int) string {
		if i < len(lines) {
			return lines[i]
		}
		return "<нет строки>"
	}
	for i := range max(len(g), len(w)) {
		if at(g, i) != at(w, i) {
			return fmt.Sprintf("line %d\n got: %s\nwant: %s", i+1, at(g, i), at(w, i))
		}
	}
	return ""
}
