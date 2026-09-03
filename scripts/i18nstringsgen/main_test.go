package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var update = os.Getenv("UPDATE_GOLDEN") != ""

// stage copies the two-language testdata catalog somewhere writable and
// generates into it, since the Go output lands beside the TOML.
func stage(t *testing.T) (dir, ts string) {
	t.Helper()
	root := t.TempDir()
	dir, ts = filepath.Join(root, "i18nstrings"), filepath.Join(root, "ts")
	for _, lang := range []string{"ru", "en"} {
		if err := os.MkdirAll(filepath.Join(dir, lang), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"board.toml", "common.toml"} {
			copyFile(t, filepath.Join("testdata/catalog", lang, name), filepath.Join(dir, lang, name))
		}
	}
	if err := os.MkdirAll(ts, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, ts
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGolden(t *testing.T) {
	dir, ts := stage(t)
	if err := generate(dir, ts, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"types_gen.go", "ru_gen.go", "en_gen.go",
		"i18nstrings_plural_gen.ts", "i18nstrings_types_gen.ts",
		"i18nstrings_ru_gen.ts", "i18nstrings_en_gen.ts",
	} {
		from := filepath.Join(dir, name)
		if strings.HasSuffix(name, ".ts") {
			from = filepath.Join(ts, name)
		}
		got, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		golden := filepath.Join("testdata/golden", name)
		if update {
			if err := os.WriteFile(golden, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("%s differs from the golden file (UPDATE_GOLDEN=1 go test ./... to refresh):\n%s", name, got)
		}
	}
}

// The generated Go has to compile, not merely parse: a struct name that
// collides or a param the two languages disagree about only shows up here.
func TestGeneratedGoCompiles(t *testing.T) {
	dir, ts := stage(t)
	if err := generate(dir, ts, false); err != nil {
		t.Fatal(err)
	}
	core, err := filepath.Abs("../../dopecore")
	if err != nil {
		t.Fatal(err)
	}
	mod := "module proof\n\ngo 1.26\n\nrequire pecheny.me/dopecore v0.0.0\n\nreplace pecheny.me/dopecore => " + core + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"mod", "tidy"}, {"build", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

func TestRejects(t *testing.T) {
	cases := map[string]string{
		"a range":         `{{range .xs}}x{{end}}`,
		"an if":           `{{if .x}}y{{end}}`,
		"a pipeline":      `{{.name | printf "%s"}}`,
		"an unknown func": `{{upper .name}}`,
		"a nested field":  `{{.a.b}}`,
		"plural arity":    `{{plural .n "one" "few"}}`,
		"plural on text":  `{{plural "x" "one" "few" "many"}}`,
		"plural forms":    `{{plural .n .a "few" "many"}}`,
	}
	for name, text := range cases {
		if _, err := compile("s.k", text); err == nil {
			t.Errorf("%s was accepted: %s", name, text)
		}
	}
}

func TestRejectsBadNames(t *testing.T) {
	root := t.TempDir()
	for _, bad := range []string{"Save", "save-now", "1st"} {
		file := filepath.Join(root, "common.toml")
		if err := os.WriteFile(file, []byte(bad+" = \"x\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := readSurface(file)
		if err == nil {
			t.Errorf("%q was accepted as a key", bad)
			continue
		}
		if !strings.Contains(err.Error(), "common.toml:1") {
			t.Errorf("%q: error does not name file:line: %v", bad, err)
		}
	}
	file := filepath.Join(root, "Common.toml")
	if err := os.WriteFile(file, []byte("save = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readSurface(file); err == nil {
		t.Error("Common.toml was accepted as a surface name")
	}
}

func TestLanguagesMustAgree(t *testing.T) {
	for _, extra := range []string{"extra = \"x\"\n", ""} {
		dir, ts := stage(t)
		path := filepath.Join(dir, "en", "common.toml")
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		body := string(b) + extra
		if extra == "" {
			body = strings.Replace(string(b), "save = \"Save\"\n", "", 1)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := generate(dir, ts, false); err == nil {
			t.Errorf("an id set that differs from ru was accepted (extra=%q)", extra)
		}
	}
}

func TestLanguagesMustAgreeOnParams(t *testing.T) {
	dir, ts := stage(t)
	path := filepath.Join(dir, "en", "board.toml")
	if err := os.WriteFile(path, []byte(`[delete]
confirm = 'Delete board {{.name}}? It holds {{plural .cards "card" "cards" "cards"}} — {{.cards}}.'
title = "Deleting a board"
percent = "100% done"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := generate(dir, ts, false)
	if err == nil || !strings.Contains(err.Error(), "board.delete.confirm") {
		t.Errorf("a renamed parameter was accepted: %v", err)
	}
}

func TestDefaultLanguageIsRequired(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "en"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := generate(root, "", false); err == nil {
		t.Error("a catalog without ru/ was accepted")
	}
}

func TestUnusedStringsFail(t *testing.T) {
	dir, ts := stage(t)
	module := filepath.Dir(dir)
	if err := os.WriteFile(filepath.Join(module, "page.go"), []byte("package p\n\nvar _ = s.Common.Save()\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := generate(dir, ts, true)
	if err == nil {
		t.Fatal("an unreferenced string was accepted")
	}
	if strings.Contains(err.Error(), "common.save") {
		t.Errorf("common.save is referenced but was reported: %v", err)
	}
	if !strings.Contains(err.Error(), "board.delete.title") {
		t.Errorf("the unreferenced ids are not named: %v", err)
	}
}

func TestReferencesFromEveryLanguage(t *testing.T) {
	dir, ts := stage(t)
	module := filepath.Dir(dir)
	files := map[string]string{
		"page.go":      "package p\n\nvar _ = s.Board.Delete.Confirm(1) + s.Board.Delete.Title()\n",
		"page.ts":      "const x = s.board.delete.percent() + s.common.selection.count(1);\n",
		"page.dopeui":  "page title=@common.save\n",
		"stale_gen.go": "package p\n\nvar _ = s.Board.Delete.Ignored()\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(module, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := generate(dir, ts, true); err != nil {
		t.Fatalf("a fully referenced catalog was rejected: %v", err)
	}
}

func TestTailOfALongerPathIsNotAReference(t *testing.T) {
	dir, ts := stage(t)
	module := filepath.Dir(dir)
	// board.delete.title is read; common.title, whose Go path is the tail of
	// that one, is not.
	extra := "[title]\nsave = 'Готово'\n"
	for _, lang := range []string{"ru", "en"} {
		f := filepath.Join(dir, lang, "common.toml")
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, append(b, []byte("\n"+extra)...), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	body := "package p\n\nvar _ = s.Board.Delete.Title()\n"
	if err := os.WriteFile(filepath.Join(module, "page.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	err := generate(dir, ts, true)
	if err == nil || !strings.Contains(err.Error(), "common.title.save") {
		t.Errorf("a tail match passed for common.title.save: %v", err)
	}
}

func TestTestFilesAreNotReferences(t *testing.T) {
	dir, ts := stage(t)
	module := filepath.Dir(dir)
	body := "package p\n\nvar _ = s.Board.Delete.Title()\n"
	if err := os.WriteFile(filepath.Join(module, "page_test.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	err := generate(dir, ts, true)
	if err == nil || !strings.Contains(err.Error(), "board.delete.title") {
		t.Errorf("a test-only reference counted: %v", err)
	}
}

func TestFailedGenerateWritesNothing(t *testing.T) {
	dir, ts := stage(t)
	if err := generate(dir, ts, true); err == nil {
		t.Fatal("an unreferenced catalog was accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "types_gen.go")); !os.IsNotExist(err) {
		t.Errorf("a failed generate left types_gen.go behind: %v", err)
	}
}

func TestBadParameterName(t *testing.T) {
	for _, tmpl := range []string{"{{.func}}", "{{.Name}}", "{{plural .type \"a\" \"b\" \"c\"}}"} {
		if _, err := compile("board.x", tmpl); err == nil {
			t.Errorf("%s was accepted as a parameter name", tmpl)
		}
	}
}
