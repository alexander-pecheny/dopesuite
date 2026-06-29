package handout

import (
	"context"
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed assets/NotoSans-Regular.ttf assets/NotoSans-Bold.ttf assets/NotoSans-Italic.ttf assets/NotoSans-BoldItalic.ttf
var fontFS embed.FS

var fontNames = []string{
	"NotoSans-Regular.ttf", "NotoSans-Bold.ttf", "NotoSans-Italic.ttf", "NotoSans-BoldItalic.ttf",
}

var (
	fontOnce sync.Once
	fontDir  string
	fontErr  error
)

// bundledFontDir materialises the embedded Noto Sans fonts to a temp dir once per
// process and returns its path (passed to typst's --font-path).
func bundledFontDir() (string, error) {
	fontOnce.Do(func() {
		d, err := os.MkdirTemp("", "xy-typst-fonts-*")
		if err != nil {
			fontErr = err
			return
		}
		for _, n := range fontNames {
			b, err := fontFS.ReadFile("assets/" + n)
			if err != nil {
				fontErr = err
				return
			}
			if err := os.WriteFile(filepath.Join(d, n), b, 0o644); err != nil {
				fontErr = err
				return
			}
		}
		fontDir = d
	})
	return fontDir, fontErr
}

// Render parses the .hndt source, emits the .typ, writes it (plus any referenced
// images, keyed by the name used in the source) to a scratch dir, and compiles a
// PDF with the typst binary at typstPath ("" → "typst" on PATH). Mirrors
// chgksuite's `typst compile --root / --font-path <fonts> source.typ source.pdf`.
func Render(ctx context.Context, hndt string, images map[string][]byte, a Args, typstPath string) ([]byte, error) {
	if typstPath == "" {
		typstPath = "typst"
	}
	dir, err := os.MkdirTemp("", "xy-handout-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	for name, data := range images {
		base := filepath.Base(name)
		if base == "" || base == "." || base == ".." || strings.ContainsAny(name, `/\`) {
			continue // names are referenced flat; ignore path-bearing keys
		}
		if err := os.WriteFile(filepath.Join(dir, base), data, 0o644); err != nil {
			return nil, err
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "source.typ"), []byte(GenerateTyp(hndt, a)), 0o644); err != nil {
		return nil, err
	}
	fonts, err := bundledFontDir()
	if err != nil {
		return nil, err
	}

	// --ignore-system-fonts: we bundle the only font we use (Noto Sans), so skip
	// scanning the OS font dirs on every invocation.
	cmd := exec.CommandContext(ctx, typstPath, "compile", "--root", "/", "--font-path", fonts, "--ignore-system-fonts", "source.typ", "source.pdf")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("typst compile failed: %s", msg)
	}
	return os.ReadFile(filepath.Join(dir, "source.pdf"))
}
