package ui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPagesCompile walks the app's .xui pages and asserts each compiles
// (parses + validates + renders) without error. Byte-for-byte parity against
// the pre-conversion HTML is checked at conversion time (see DESIGN.md), not
// here — this just guards against future edits breaking a page.
func TestPagesCompile(t *testing.T) {
	pages, err := filepath.Glob("../../web/assets/ui/*.xui")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("no .xui pages found")
	}
	for _, path := range pages {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if _, err := Compile(filepath.Base(path), src); err != nil {
				t.Fatalf("compile: %v", err)
			}
		})
	}
}
