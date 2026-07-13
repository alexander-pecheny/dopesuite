package kit

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "regenerate golden .html files")

// TestFixtures compiles every testdata/*.dopeui (core vocabulary) and diffs
// against its golden .html (regenerate with -run TestFixtures -update).
func TestFixtures(t *testing.T) {
	fixtures, err := filepath.Glob("testdata/*.dopeui")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no fixtures found")
	}
	for _, path := range fixtures {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			got, err := Compile(filepath.Base(path), src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			golden := strings.TrimSuffix(path, ".dopeui") + ".html"
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run -update): %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("output mismatch for %s; run: go test ./ui -run TestFixtures -update", path)
			}
		})
	}
}
