package handout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTypParity generates the .typ for each testdata/*.hndt and compares it,
// byte-for-byte, to chgksuite's own output (testdata/*.typ). Since the layout is
// done by the shared Typst template, identical .typ ⇒ identical PDF.
// Regenerate oracles with: chgksuite handouts hndt2pdf <file> (writes <bn>_ru.typ).
func TestTypParity(t *testing.T) {
	files, err := filepath.Glob("testdata/*.hndt")
	if err != nil || len(files) == 0 {
		t.Fatalf("no testdata: %v", err)
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".hndt")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			oracle, err := os.ReadFile(filepath.Join("testdata", name+".typ"))
			if err != nil {
				t.Skipf("no oracle for %s", name)
			}
			got := GenerateTyp(string(src), DefaultArgs())
			// chgksuite writes the file without a trailing newline; tolerate one.
			if strings.TrimRight(got, "\n") != strings.TrimRight(string(oracle), "\n") {
				t.Errorf(".typ mismatch for %s\n--- chgksuite ---\n%s\n--- go ---\n%s",
					name, tailLines(string(oracle)), tailLines(got))
			}
		})
	}
}

// tailLines returns the last few lines (the per-question content; the header is
// identical) to keep failure output readable.
func tailLines(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	return strings.Join(lines, "\n")
}
