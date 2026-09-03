package stats

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"strconv"
	"strings"

	corei18n "pecheny.me/dopecore/i18nstrings"
	xystrings "xy/i18nstrings"
)

// The xlsx side of the question table. openpyxl reads these for chgksuite;
// a results table is a grid of numbers and team names on one sheet, so this
// reads that much of SpreadsheetML directly rather than adding a spreadsheet
// library to the module.

// ReadXLSX reads the workbook's first sheet as rows of cell text.
func ReadXLSX(path string) ([]Result, []string, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, nil, err
	}
	defer z.Close()
	shared, err := sharedStrings(&z.Reader)
	if err != nil {
		return nil, nil, err
	}
	name, err := firstSheetPart(&z.Reader)
	if err != nil {
		return nil, nil, err
	}
	rows, err := sheetRows(&z.Reader, name, shared)
	if err != nil {
		return nil, nil, err
	}
	results, warnings := ReadTable(rows)
	return results, warnings, nil
}

func openPart(z *zip.Reader, name string) (io.ReadCloser, error) {
	for _, f := range z.File {
		if f.Name == name {
			return f.Open()
		}
	}
	return nil, corei18n.User(xystrings.Default.Stats.Xlsx.PartMissing(name))
}

// firstSheetPart follows workbook.xml's first <sheet> through the rels to its
// part, rather than assuming sheet1.xml.
func firstSheetPart(z *zip.Reader) (string, error) {
	wb, err := openPart(z, "xl/workbook.xml")
	if err != nil {
		return "", err
	}
	defer wb.Close()
	var book struct {
		Sheets []struct {
			ID string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.NewDecoder(wb).Decode(&book); err != nil {
		return "", err
	}
	if len(book.Sheets) == 0 {
		return "", corei18n.User(xystrings.Default.Stats.Xlsx.NoSheets())
	}
	rels, err := openPart(z, "xl/_rels/workbook.xml.rels")
	if err != nil {
		return "", err
	}
	defer rels.Close()
	var doc struct {
		Rel []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if err := xml.NewDecoder(rels).Decode(&doc); err != nil {
		return "", err
	}
	for _, r := range doc.Rel {
		if r.ID == book.Sheets[0].ID {
			return "xl/" + strings.TrimPrefix(r.Target, "/xl/"), nil
		}
	}
	return "", corei18n.User(xystrings.Default.Stats.Xlsx.SheetMissing(book.Sheets[0].ID))
}

func sharedStrings(z *zip.Reader) ([]string, error) {
	part, err := openPart(z, "xl/sharedStrings.xml")
	if err != nil {
		return nil, nil //nolint:nilerr // a workbook without one only has inline strings
	}
	defer part.Close()
	var doc struct {
		Items []struct {
			Runs []string `xml:"r>t"`
			Text string   `xml:"t"`
		} `xml:"si"`
	}
	if err := xml.NewDecoder(part).Decode(&doc); err != nil {
		return nil, err
	}
	out := make([]string, len(doc.Items))
	for i, it := range doc.Items {
		out[i] = it.Text + strings.Join(it.Runs, "")
	}
	return out, nil
}

func sheetRows(z *zip.Reader, name string, shared []string) ([][]string, error) {
	part, err := openPart(z, name)
	if err != nil {
		return nil, err
	}
	defer part.Close()
	var doc struct {
		Rows []struct {
			Cells []struct {
				Ref    string `xml:"r,attr"`
				Type   string `xml:"t,attr"`
				Value  string `xml:"v"`
				Inline string `xml:"is>t"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := xml.NewDecoder(part).Decode(&doc); err != nil {
		return nil, err
	}
	var rows [][]string
	for _, r := range doc.Rows {
		var row []string
		for _, c := range r.Cells {
			col := columnIndex(c.Ref)
			if col < 0 {
				continue
			}
			for len(row) <= col {
				row = append(row, "")
			}
			switch c.Type {
			case "s":
				if i, err := strconv.Atoi(c.Value); err == nil && i < len(shared) {
					row[col] = shared[i]
				}
			case "inlineStr":
				row[col] = c.Inline
			default:
				row[col] = c.Value
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// columnIndex turns the letters of a cell reference into its 0-based column.
func columnIndex(ref string) int {
	n := 0
	for _, c := range ref {
		if c < 'A' || c > 'Z' {
			break
		}
		n = n*26 + int(c-'A') + 1
	}
	return n - 1
}
