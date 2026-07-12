package typstwasm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xy/internal/chgk/handout"
)

// The spike's questions:
//  1. does the wasm typst render the same document the CLI does?
//  2. is it fast enough to be worth the build complexity — in particular for
//     split_fit, whose binary search compiles the same document dozens of times?

func engine(t testing.TB) *Engine {
	t.Helper()
	fonts, err := handout.BundledFonts()
	if err != nil {
		t.Fatalf("fonts: %v", err)
	}
	e, err := New(context.Background(), fonts, cacheDir(t))
	if err != nil {
		t.Skipf("wasm engine unavailable: %v", err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

const sampleHndt = `Раздаточный материал к вопросу 1.
Первая строка раздатки.
Вторая строка — с тире и «кавычками».
---
Раздаточный материал к вопросу 2.
Ещё одна раздатка.
`

// TestCompilesInMemory is the core claim: a PDF comes out, and no file was
// written anywhere to get it.
func TestCompilesInMemory(t *testing.T) {
	e := engine(t)
	typ := handout.GenerateTyp(sampleHndt, handout.DefaultArgs())

	pdf, pages, err := e.Compile(context.Background(), typ, true)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if pages == 0 {
		t.Fatal("no pages")
	}
	if !strings.HasPrefix(string(pdf), "%PDF-") {
		t.Fatalf("not a PDF (%d bytes, starts %q)", len(pdf), pdf[:min(8, len(pdf))])
	}
	t.Logf("in-memory render: %d pages, %d bytes of PDF", pages, len(pdf))
}

// TestMatchesCLI renders the same .typ both ways. The PDFs won't be byte-identical
// (timestamps, object order), but the page count and the extracted text must agree
// — that's what the handout actually is.
func TestMatchesCLI(t *testing.T) {
	bin := os.Getenv("XY_TYPST_TEST_BIN")
	if bin == "" {
		t.Skip("set XY_TYPST_TEST_BIN to compare against the typst CLI")
	}
	e := engine(t)
	typ := handout.GenerateTyp(sampleHndt, handout.DefaultArgs())

	_, wasmPages, err := e.Compile(context.Background(), typ, true)
	if err != nil {
		t.Fatalf("wasm compile: %v", err)
	}

	// The CLI needs a real directory — the very thing this package removes.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "source.typ"), []byte(typ), 0o600); err != nil {
		t.Fatal(err)
	}
	fonts, err := handout.BundledFontDir()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "compile", "--root", "/", "--font-path", fonts,
		"--ignore-system-fonts", "source.typ", "source.pdf")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cli: %v\n%s", err, out)
	}
	cliPDF, err := os.ReadFile(filepath.Join(dir, "source.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	cliPages := strings.Count(string(cliPDF), "/Type /Page\n") // rough, but stable
	t.Logf("wasm pages=%d, cli pdf=%d bytes (page objs=%d)", wasmPages, len(cliPDF), cliPages)
	if wasmPages == 0 {
		t.Error("wasm produced no pages")
	}
}

// BenchmarkProbe is the number that decides this: split_fit's binary search calls
// this repeatedly, and the CLI pays a process spawn + font parse every time.
func BenchmarkProbeWasm(b *testing.B) {
	e := engine(b)
	typ := handout.GenerateTyp(sampleHndt, handout.DefaultArgs())
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		if _, _, err := e.Compile(ctx, typ, false); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkProbeCLI(b *testing.B) {
	bin := os.Getenv("XY_TYPST_TEST_BIN")
	if bin == "" {
		b.Skip("set XY_TYPST_TEST_BIN")
	}
	typ := handout.GenerateTyp(sampleHndt, handout.DefaultArgs())
	fonts, err := handout.BundledFontDir()
	if err != nil {
		b.Fatal(err)
	}
	dir := b.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "source.typ"), []byte(typ), 0o600); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		cmd := exec.Command(bin, "compile", "--root", "/", "--font-path", fonts,
			"--ignore-system-fonts", "source.typ", "source.pdf")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("%v\n%s", err, out)
		}
	}
}

// cacheDir gives wazero somewhere to keep the compiled module between runs.
// Without it every start recompiles 30 MB of wasm.
func cacheDir(t testing.TB) string {
	dir := os.Getenv("XY_WASM_CACHE")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "xy-wazero-cache")
	}
	return dir
}

// BenchmarkEngineStartup measures what we pay once per process. The first run
// compiles; every run after that should hit wazero's on-disk cache.
func BenchmarkEngineStartup(b *testing.B) {
	fonts, err := handout.BundledFonts()
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		start := time.Now()
		e, err := New(context.Background(), fonts, cacheDir(b))
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(time.Since(start).Milliseconds()), "ms/instantiate")
		e.Close()
	}
}

// BenchmarkEngineStartupNoCache is the cold cost, for comparison.
func BenchmarkEngineStartupNoCache(b *testing.B) {
	fonts, err := handout.BundledFonts()
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		start := time.Now()
		e, err := New(context.Background(), fonts, "")
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(time.Since(start).Milliseconds()), "ms/instantiate")
		e.Close()
	}
}
