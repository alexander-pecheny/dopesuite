package handout

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"xy/internal/chgk/typstwasm"
)

// The wasm typesetter reads the page count straight off the laid-out document
// (doc.pages().len()), where the CLI asks typst to query a `<xypages>` metadata
// label. Those are two different questions, and split_fit's binary search fits the
// densest one-page layout by asking them — so if they ever disagree, every handout
// comes out with the wrong number of rows.
//
// This is the test that had to pass before the wasm path could replace the CLI.

func wasmTS(t testing.TB) *typstwasm.Pool {
	t.Helper()
	fonts, err := BundledFonts()
	if err != nil {
		t.Fatal(err)
	}
	cache := os.Getenv("XY_WASM_CACHE")
	if cache == "" {
		cache = filepath.Join(os.TempDir(), "xy-wazero-cache")
	}
	p, err := typstwasm.NewPool(context.Background(), fonts, cache, 4)
	if err != nil {
		t.Fatalf("wasm pool: %v", err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

// splitFitParitySrc exercises the interesting cases: a tiny block, a long one that
// must shrink, a multi-column team handout, and an explicit row count.
const splitFitParitySrc = "for_question: 1\ncolumns: 3\n\nКороткий\n" +
	"---\nfor_question: 7\ncolumns: 3\n\nПобольше текста для второго вопроса, чтобы он занял заметно больше места и поместилось меньше строк на странице.\n" +
	"---\nfor_question: 13\ncolumns: 6\nhandouts_per_team: 2\n\nКоманде\n" +
	"---\nfor_question: 21\ncolumns: 2\n\nЕщё один блок с текстом подлиннее, чтобы проверить, что подбор строк совпадает.\n"

// TestWasmMatchesCLIRows is the parity gate: the fitted row counts must be
// identical, block for block.
func TestWasmMatchesCLIRows(t *testing.T) {
	bin := os.Getenv("XY_TYPST_TEST_BIN")
	if bin == "" {
		t.Skip("set XY_TYPST_TEST_BIN to compare the wasm typesetter against the CLI")
	}
	ctx := context.Background()

	cli, err := NewCLITypesetter(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	cliRows, err := FitRows(ctx, splitFitParitySrc, nil, DefaultArgs(), cli)
	if err != nil {
		t.Fatalf("cli FitRows: %v", err)
	}
	wasmRows, err := FitRows(ctx, splitFitParitySrc, nil, DefaultArgs(), wasmTS(t))
	if err != nil {
		t.Fatalf("wasm FitRows: %v", err)
	}
	t.Logf("cli  rows: %v", cliRows)
	t.Logf("wasm rows: %v", wasmRows)
	if !reflect.DeepEqual(cliRows, wasmRows) {
		t.Fatalf("row counts differ — the wasm page count is not the CLI's <xypages>\n cli=%v\nwasm=%v", cliRows, wasmRows)
	}
}

// TestWasmSplitFitProducesOnePagePDFs re-checks split_fit's invariant through the
// wasm path: every per-question handout is exactly one page.
func TestWasmSplitFitProducesOnePagePDFs(t *testing.T) {
	zipBytes, err := SplitFit(context.Background(), splitFitParitySrc, nil, DefaultArgs(), wasmTS(t))
	if err != nil {
		t.Fatalf("SplitFit: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("zip: %v", err)
	}
	if len(zr.File) == 0 {
		t.Fatal("empty zip")
	}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		buf.ReadFrom(rc)
		rc.Close()
		if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF")) {
			t.Errorf("%s is not a PDF", f.Name)
		}
	}
	t.Logf("split_fit produced %d PDFs, entirely in memory", len(zr.File))
}

// TestWasmRenderWithImage proves an image handout works with the images served
// from memory — the case that would otherwise need files on disk.
func TestWasmRenderWithImage(t *testing.T) {
	// A 1x1 PNG.
	png := []byte{
		0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R',
		0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89,
		0, 0, 0, 0x0b, 'I', 'D', 'A', 'T', 0x78, 0x9c, 0x63, 0xfc, 0xcf, 0xc0, 0x50, 0x0f,
		0, 0x04, 0x85, 0x01, 0x80, 0x84, 0xa9, 0x8c, 0x21, 0, 0, 0, 0, 'I', 'E', 'N', 'D',
		0xae, 0x42, 0x60, 0x82,
	}
	hndt := "for_question: 3\ncolumns: 1\n\n(img pic.png)\n"
	pdf, err := Render(context.Background(), hndt, map[string][]byte{"pic.png": png}, DefaultArgs(), wasmTS(t))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatal("not a PDF")
	}
	t.Logf("rendered a %d-byte PDF with an in-memory image", len(pdf))
}
