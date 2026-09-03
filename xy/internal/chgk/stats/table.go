package stats

import (
	"encoding/csv"
	"os"
	"strconv"
	"strings"

	xystrings "xy/i18nstrings"
)

// ReadTable ports _results_table_to_masks: the two layouts rating.chgk.info
// exports a question table in — one row per team, or one row per team per
// tour — under a header row whose second column is the team-name column.
// Anything before that header is skipped, and any cell that is neither 0 nor 1
// (an unresolved controversy, say) counts as not taken and is reported rather
// than refused.
func ReadTable(rows [][]string) ([]Result, []string) {
	s := xystrings.Default
	type key struct {
		id   int
		name string
	}
	var order []key
	byTeam := map[key]map[int][]byte{}
	tourLen := map[int]int{}
	var tours []int
	layout := ""
	var disputedN int
	var disputedTeam, disputedValue string

	for _, row := range rows {
		if allBlank(row) {
			continue
		}
		if layout == "" {
			if len(row) > 1 && strings.TrimSpace(row[1]) == "Название" {
				layout = "full"
				if len(row) > 3 && row[3] == "Тур" {
					layout = "tour"
				}
			}
			continue
		}
		id, err := strconv.Atoi(strings.TrimSpace(row[0]))
		if err != nil || len(row) < 2 {
			continue
		}
		tour, from := 1, 3
		if layout == "tour" {
			tour, from = 0, 4
			if len(row) > 3 {
				tour, _ = strconv.Atoi(strings.TrimSpace(row[3]))
			}
		}
		var mask []byte
		if from < len(row) {
			for _, cell := range row[from:] {
				switch strings.TrimSpace(cell) {
				case "1":
					mask = append(mask, '1')
				case "0", "":
					mask = append(mask, '0')
				default:
					mask = append(mask, '0')
					disputedN++
					if disputedN == 1 {
						disputedTeam, disputedValue = row[1], cell
					}
				}
			}
		}
		if _, seen := tourLen[tour]; !seen {
			tours = append(tours, tour)
		}
		tourLen[tour] = max(tourLen[tour], len(mask))
		k := key{id, row[1]}
		if byTeam[k] == nil {
			byTeam[k] = map[int][]byte{}
			order = append(order, k)
		}
		byTeam[k][tour] = mask
	}

	if layout == "" {
		return nil, []string{s.Stats.Table.HeaderMissing()}
	}
	var warnings []string
	if disputedN > 0 {
		warnings = append(warnings, s.Stats.Table.Disputed(
			strconv.Itoa(disputedN), disputedValue, disputedTeam))
	}
	slicesSort(tours)
	results := make([]Result, 0, len(order))
	for _, k := range order {
		var mask []byte
		for _, tour := range tours {
			m := byTeam[k][tour]
			mask = append(mask, m...)
			for i := len(m); i < tourLen[tour]; i++ {
				mask = append(mask, '0')
			}
		}
		results = append(results, Result{TeamID: k.id, Name: k.name, Mask: string(mask)})
	}
	return results, warnings
}

// ReadCSV reads a question table exported as csv. The delimiter is what
// chgksuite takes as --custom_csv_args {"delimiter": ";"}; a leading BOM is
// dropped, as Python's utf-8-sig does.
func ReadCSV(path string, delimiter rune) ([]Result, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(data), "\ufeff")))
	r.Comma = delimiter
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	rows, err := r.ReadAll()
	if err != nil {
		return nil, nil, err
	}
	results, warnings := ReadTable(rows)
	return results, warnings, nil
}

// ReadFile reads either layout from either format, by extension, the way
// add_stats picks between custom_csv_to_results and xlsx_to_results.
func ReadFile(path string, delimiter rune) ([]Result, []string, error) {
	if strings.EqualFold(pathExt(path), ".xlsx") {
		return ReadXLSX(path)
	}
	return ReadCSV(path, delimiter)
}

func allBlank(row []string) bool {
	for _, c := range row {
		if c != "" {
			return false
		}
	}
	return true
}

func slicesSort(xs []int) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

func pathExt(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i:]
	}
	return ""
}
