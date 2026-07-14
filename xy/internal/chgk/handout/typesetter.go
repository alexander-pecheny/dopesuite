package handout

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
)

// Typesetter compiles a typst document. It exists so handout rendering doesn't
// have to care whether typst is a child process reading files or a wasm module
// reading memory.
//
// The in-memory implementation (internal/chgk/typstwasm) is what the server uses:
// typst is the only thing in xy that would otherwise force the user's decrypted
// questions onto a filesystem. CLITypesetter below drives the typst binary the old
// way, and is kept because it is the oracle the wasm path is checked against.
type Typesetter interface {
	// SetImages replaces the images the source may reference, by bare name.
	SetImages(ctx context.Context, images map[string][]byte) error
	// Compile typesets typ. When wantPDF is false only the page count is needed
	// (split_fit's binary search), and the PDF may be skipped.
	Compile(ctx context.Context, typ string, wantPDF bool) (pdf []byte, pages int, err error)
}

// CLITypesetter shells out to the typst binary. It needs a real directory —
// precisely the property the wasm typesetter exists to remove — so it writes the
// plaintext into a RAM-backed scratch dir and wipes it on Close.
type CLITypesetter struct {
	bin   string
	dir   string
	fonts string
	seq   atomic.Int64
}

// NewCLITypesetter prepares a scratch dir for the typst binary at bin
// ("" → "typst" on PATH). Call Close to wipe it.
func NewCLITypesetter(bin string) (*CLITypesetter, error) {
	if bin == "" {
		bin = "typst"
	}
	fonts, err := bundledFontDir()
	if err != nil {
		return nil, err
	}
	dir, err := scratchTemp("xy-typst-*")
	if err != nil {
		return nil, err
	}
	return &CLITypesetter{bin: bin, dir: dir, fonts: fonts}, nil
}

func (c *CLITypesetter) Close() error { return os.RemoveAll(c.dir) }

func (c *CLITypesetter) SetImages(_ context.Context, images map[string][]byte) error {
	for name, data := range images {
		base := filepath.Base(name)
		if base == "" || base == "." || base == ".." || strings.ContainsAny(name, `/\`) {
			continue // names are referenced flat; ignore path-bearing keys
		}
		if err := writeScratch(c.dir, base, data); err != nil {
			return err
		}
	}
	return nil
}

func (c *CLITypesetter) Compile(ctx context.Context, typ string, wantPDF bool) ([]byte, int, error) {
	name := fmt.Sprintf("sf_%d.typ", c.seq.Add(1))
	if err := writeScratch(c.dir, name, []byte(typ)); err != nil {
		return nil, 0, err
	}
	defer os.Remove(filepath.Join(c.dir, name))

	if !wantPDF {
		// Ask typst itself how many pages it paginates to, without building a PDF.
		cmd := exec.CommandContext(ctx, c.bin, "query", "--root", "/", "--font-path", c.fonts,
			"--ignore-system-fonts", name, "<xypages>", "--field", "value", "--one")
		cmd.Dir = c.dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return nil, 0, fmt.Errorf("typst query: %s", strings.TrimSpace(string(out)))
		}
		// The value, possibly followed by a deprecation warning.
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if n, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
				return nil, n, nil
			}
		}
		return nil, 0, fmt.Errorf("typst query: unparseable page count %q", string(out))
	}

	pdfName := strings.TrimSuffix(name, ".typ") + ".pdf"
	cmd := exec.CommandContext(ctx, c.bin, "compile", "--root", "/", "--font-path", c.fonts,
		"--ignore-system-fonts", name, pdfName)
	cmd.Dir = c.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, 0, fmt.Errorf("typst compile: %s", strings.TrimSpace(string(out)))
	}
	pdfPath := filepath.Join(c.dir, pdfName)
	defer os.Remove(pdfPath)
	raw, err := os.ReadFile(pdfPath)
	if err != nil {
		return nil, 0, err
	}
	return raw, 0, nil // the CLI's page count is only asked for via the query above
}
