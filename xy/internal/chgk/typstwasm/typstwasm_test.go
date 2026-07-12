package typstwasm_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xy/internal/chgk/handout"
	"xy/internal/chgk/typstwasm"
)

// cacheDir gives wazero somewhere to keep typst compiled to machine code. Without
// it every run recompiles 30 MB of wasm (~15s); with it, ~0.5s.
func cacheDir() string {
	if dir := os.Getenv("XY_WASM_CACHE"); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "xy-wazero-cache")
}

func pool(t testing.TB, size int) *typstwasm.Pool {
	t.Helper()
	fonts, err := handout.BundledFonts()
	if err != nil {
		t.Fatalf("fonts: %v", err)
	}
	p, err := typstwasm.NewPool(context.Background(), fonts, cacheDir(), size)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

const sampleHndt = `for_question: 1
columns: 3

Первая раздатка — с тире и «кавычками».
---
for_question: 2
columns: 3

Ещё одна раздатка.
`

// TestCompilesInMemory is the core claim: a PDF comes out, and not one file was
// written anywhere to produce it.
func TestCompilesInMemory(t *testing.T) {
	p := pool(t, 1)
	typ := handout.GenerateTyp(sampleHndt, handout.DefaultArgs())

	pdf, pages, err := p.Compile(context.Background(), typ, true)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if pages == 0 {
		t.Fatal("no pages")
	}
	if !strings.HasPrefix(string(pdf), "%PDF-") {
		t.Fatalf("not a PDF (%d bytes)", len(pdf))
	}
	t.Logf("in-memory render: %d pages, %d bytes of PDF", pages, len(pdf))
}

// TestPoolIsConcurrent checks the pool actually lets renders overlap — split_fit
// fits its blocks in parallel, and a single serialised instance would undo that.
func TestPoolIsConcurrent(t *testing.T) {
	p := pool(t, 4)
	typ := handout.GenerateTyp(sampleHndt, handout.DefaultArgs())
	ctx := context.Background()

	errs := make(chan error, 8)
	start := time.Now()
	for range 8 {
		go func() {
			_, _, err := p.Compile(ctx, typ, false)
			errs <- err
		}()
	}
	for range 8 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent compile: %v", err)
		}
	}
	t.Logf("8 concurrent compiles across 4 instances in %v", time.Since(start).Round(time.Millisecond))
}

// BenchmarkProbeWasm is the number that justifies all this: split_fit's binary
// search calls it dozens of times per generation.
func BenchmarkProbeWasm(b *testing.B) {
	p := pool(b, 1)
	typ := handout.GenerateTyp(sampleHndt, handout.DefaultArgs())
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		if _, _, err := p.Compile(ctx, typ, false); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkProbeCLI is the same probe the old way: a fresh process that re-reads
// the fonts every time.
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

// BenchmarkPoolStartup is the once-per-process cost. Cold (no cache) it is a ~15s
// wasm compile; warm it should be well under a second.
func BenchmarkPoolStartup(b *testing.B) {
	fonts, err := handout.BundledFonts()
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		start := time.Now()
		p, err := typstwasm.NewPool(context.Background(), fonts, cacheDir(), 4)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(time.Since(start).Milliseconds()), "ms/startup")
		p.Close()
	}
}
