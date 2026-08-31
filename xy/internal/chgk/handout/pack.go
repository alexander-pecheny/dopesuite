package handout

import (
	"bytes"
	"errors"
	"io"
	"math"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// The `pack` half of handouts (chgksuite/handouter/pack.py): a folder of
// split-fitted single-handout files, each printed as many times as the field
// needs, merged into one PDF for the colour printer and one for the other.

// ImageNames lists the pictures a .hndt names, so a caller can read them off
// disk before handing them to the renderer.
func ImageNames(hndt string) []string {
	var out []string
	seen := map[string]bool{}
	for _, b := range parseHandouts(hndt) {
		name, ok := b.str("image")
		if !ok || name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// PackPages is pack_handouts's arithmetic: how many copies of this file's one
// page the field needs, and whether it goes to the colour printer. ok is false
// for a file that holds no handout, or more than one — pack only takes the
// split-fitted single-handout files.
func PackPages(hndt string, teams int) (pages int, colour bool, err error) {
	var only block
	for _, b := range parseHandouts(hndt) {
		if len(b) == 0 {
			continue
		}
		if only != nil {
			return 0, false, errors.New("больше одной раздатки: pack берёт только файлы после split_fit")
		}
		only = b
	}
	if only == nil {
		return 0, false, errors.New("в файле нет раздаток")
	}
	perTeam := 3
	if v, ok := only.intVal("handouts_per_team"); ok && v != 0 {
		perTeam = v
	}
	columns, _ := only.intVal("columns")
	rows, _ := only.intVal("rows")
	perPage := columns * rows
	if perPage == 0 {
		return 0, false, errors.New("нет columns или rows: pack берёт только файлы после split_fit")
	}
	if c, ok := only.intVal("color"); ok && c != 0 {
		colour = true
	}
	teamsPerPage := float64(perPage) / float64(perTeam)
	return int(math.Ceil(float64(teams+1) / teamsPerPage)), colour, nil
}

// MergePDFs concatenates the pages in order, optionally compressing the result
// the way every other PDF this package writes is compressed.
func MergePDFs(pdfs [][]byte, compress bool) ([]byte, error) {
	inputs := make([]io.ReadSeeker, len(pdfs))
	for i, p := range pdfs {
		inputs[i] = bytes.NewReader(p)
	}
	var out bytes.Buffer
	if err := api.MergeRaw(inputs, &out, false, pdfConf()); err != nil {
		return nil, err
	}
	if compress {
		return compressPDF(out.Bytes()), nil
	}
	return out.Bytes(), nil
}
