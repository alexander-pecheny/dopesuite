package replay

import (
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// The committed transcripts are the oracle, so they must stay readable — by the
// parser and, more importantly, by a person. This test is what stops a reader
// script from quietly emitting something nobody can check.
func TestStudchrTranscriptsParse(t *testing.T) {
	paths, err := filepath.Glob("../../../testdata/*/*.transcript")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Skip("стенограмм ещё нет")
	}
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		script, err := Parse(string(src))
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(path), err)
			continue
		}
		if script.Game == "" {
			t.Errorf("%s: не сказано, что за игра", filepath.Base(path))
		}
		seen := map[string]int{}
		for _, bout := range script.Bouts {
			if prev, ok := seen[bout.At.String()]; ok {
				t.Errorf("%s: координата %s занята дважды, строки %d и %d",
					filepath.Base(path), bout.At, prev, bout.Line)
			}
			seen[bout.At.String()] = bout.Line
			if len(bout.Seats) < 2 {
				t.Errorf("%s: в бою %s %d мест", filepath.Base(path), bout.At, len(bout.Seats))
			}
		}
	}
}

// ЭК СтудЧР-2026 в цифрах: 48 команд, 25 боёв, 96 мест. Если читатель листа
// однажды съедет на строку, это заметит здесь, а не через полтурнира.
func TestStudchrEKTranscriptShape(t *testing.T) {
	src, err := os.ReadFile("../../../testdata/studchr2026/ek.transcript")
	if err != nil {
		t.Skip("стенограммы ЭК ещё нет")
	}
	script, err := Parse(string(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(script.Bouts) != 25 {
		t.Errorf("боёв = %d, want 25", len(script.Bouts))
	}
	seats := 0
	for _, bout := range script.Bouts {
		seats += len(bout.Seats)
		for _, seat := range bout.Seats {
			if len(seat.Marks) != 12 {
				t.Fatalf("%s, %s: тем = %d, want 12", bout.At, seat.Name, len(seat.Marks))
			}
		}
	}
	if seats != 96 {
		t.Errorf("мест сыграно = %d, want 96", seats)
	}
	// Первый круг — двенадцать боёв в два захода по шесть столов.
	waves := map[int]int{}
	for _, bout := range script.Bouts {
		if bout.At.Round == 1 {
			waves[bout.At.Wave]++
		}
	}
	if len(waves) != 2 || waves[1] != 6 || waves[2] != 6 {
		t.Errorf("1/16 финала = %v, want два захода по шесть", waves)
	}
}

var updateGolden = flag.Bool("update", false, "rewrite docs/studchr2026-discrepancies.md from the transcripts")

// The discrepancies page is generated from the transcripts' overrides and
// committed under docs/, so a reviewed deviation is never hand-kept. This test
// keeps the two in step: run with -update to regenerate the page.
func TestStudchrDiscrepanciesPage(t *testing.T) {
	paths, err := filepath.Glob("../../../testdata/studchr2026/*.transcript")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Skip("стенограмм ещё нет")
	}
	sort.Strings(paths)
	var scripts []Script
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		script, err := Parse(string(src))
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(path), err)
		}
		scripts = append(scripts, script)
	}
	page := Discrepancies(scripts...)
	const golden = "../../../docs/studchr2026-discrepancies.md"
	if *updateGolden {
		if err := os.WriteFile(golden, []byte(page), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	have, err := os.ReadFile(golden)
	if err != nil || string(have) != page {
		t.Fatalf("%s отстала от стенограмм — перегенерируйте: go test ./dope/domain/replay -run Discrepancies -update", golden)
	}
}
