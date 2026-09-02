package xlsxexport

import (
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

// The two workbooks in testdata/ are what rating.chgk.info produces for
// tournament 10233; these tests pin the layout dope has to write — the header
// row and the shape of a data row — not their data.
func referenceRow(t *testing.T, name string, row int) []string {
	t.Helper()
	f, err := excelize.OpenFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer f.Close()
	rows, err := f.GetRows("Worksheet")
	if err != nil {
		t.Fatalf("rows of %s: %v", name, err)
	}
	if len(rows) <= row {
		t.Fatalf("%s has %d rows", name, len(rows))
	}
	return rows[row]
}

func writtenRow(t *testing.T, f *excelize.File, row int) []string {
	t.Helper()
	rows, err := f.GetRows("Worksheet")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) <= row {
		t.Fatalf("sheet has %d rows, wanted row %d", len(rows), row)
	}
	return rows[row]
}

func TestODPlayersSheetMatchesTheReferenceLayout(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	rows := []ODPlayerRow{
		{Place: "1", TeamID: 62868, Name: "Gay Guerrilla", City: "сборная", Flag: "К",
			PlayerID: 24850, Surname: "Печеный", FirstName: "Александр", Patronymic: "Павлович"},
		{Place: "2–3", TeamID: 0, Name: "Разовая", City: "Тбилиси", Flag: "Б",
			PlayerID: 0, Surname: "Иванов", FirstName: "Иван", Patronymic: ""},
	}
	if err := BuildODPlayersSheet(f, rows); err != nil {
		t.Fatal(err)
	}
	want := referenceRow(t, "tournament-with-players-10233.xlsx", 0)
	got := writtenRow(t, f, 0)
	if len(got) != len(want) {
		t.Fatalf("header %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("header[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	first := writtenRow(t, f, 1)
	if len(first) != len(want) {
		t.Fatalf("row %v has %d cells, want %d", first, len(first), len(want))
	}
	if first[0] != "1" || first[1] != "62868" || first[4] != "К" || first[5] != "24850" {
		t.Fatalf("row %v", first)
	}
	// A tie keeps its label rather than becoming a number.
	if second := writtenRow(t, f, 2); second[0] != "2–3" || second[1] != "0" {
		t.Fatalf("tie row %v", second)
	}
}

func TestODToursSheetMatchesTheReferenceLayout(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	scheme := `{"gameType":"od","tourComp":[12,12,12]}`
	state := `{"teams":[{"name":"Gay Guerrilla","city":"сборная","number":1}],"entries":[],"completed":[]}`
	if err := BuildODSheet(f, scheme, state, map[int64]int64{1: 62868}); err != nil {
		t.Fatal(err)
	}
	want := referenceRow(t, "tournament-tours-10233.xlsx", 1)
	got := writtenRow(t, f, 1)
	if len(got) != len(want) {
		t.Fatalf("header %v (%d cells), want %v (%d)", got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("header[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if row := writtenRow(t, f, 2); row[0] != "62868" || row[3] != "1" {
		t.Fatalf("first data row %v", row)
	}
}
