package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSkips(t *testing.T) {
	for _, name := range []string{"ru_gen.go", "palette_gen.ts", "board_test.go", "board.test.ts"} {
		if !skipFile(name) {
			t.Errorf("%s is linted", name)
		}
	}
	for _, name := range []string{"board.go", "board.ts", "board.dopeui"} {
		if skipFile(name) || !linted(name) {
			t.Errorf("%s is not linted", name)
		}
	}
	for _, name := range []string{"board.md", "board.json", "board.toml"} {
		if linted(name) {
			t.Errorf("%s is linted", name)
		}
	}
	if !skipPath("xy/internal/chgk/i18n/assets/labels_ru.toml") || !skipPath("xy/web/ts/towns.ts") {
		t.Error("a parity asset is linted")
	}
}

func TestScanFindsTheFirstCyrillicLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.go")
	if err := os.WriteFile(path, []byte("package x\n\n// fine\nconst s = \"Отмена\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := scan(path)
	if err != nil {
		t.Fatal(err)
	}
	if h == nil || h.line != 4 {
		t.Fatalf("scan = %+v", h)
	}
	if err := os.WriteFile(path, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if h, err := scan(path); err != nil || h != nil {
		t.Errorf("clean file reported: %+v %v", h, err)
	}
}

func TestReadAllowlist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allowlist.txt")
	if err := os.WriteFile(path, []byte("# a note\n\nxy/a.go\n  dope/b.ts  # why\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readAllowlist(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got["xy/a.go"] || !got["dope/b.ts"] {
		t.Errorf("allowlist = %v", got)
	}
}
