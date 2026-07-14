package handout

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
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

// Render typesets the .hndt source to a PDF. images are the pictures it
// references, by the name used in the source.
//
// ts decides where typst runs: the server passes the in-memory wasm typesetter, so
// the decrypted questions never reach a filesystem.
func Render(ctx context.Context, hndt string, images map[string][]byte, a Args, ts Typesetter) ([]byte, error) {
	if err := ts.SetImages(ctx, images); err != nil {
		return nil, err
	}
	pdf, _, err := ts.Compile(ctx, GenerateTyp(hndt, a), true)
	if err != nil {
		return nil, fmt.Errorf("typst compile failed: %w", err)
	}
	return pdf, nil
}

// BundledFonts returns the embedded Noto Sans faces as bytes. The wasm typst
// (internal/chgk/typstwasm) serves fonts from memory, so it needs the bytes rather
// than a directory to point --font-path at.
func BundledFonts() ([][]byte, error) {
	out := make([][]byte, 0, len(fontNames))
	for _, n := range fontNames {
		b, err := fontFS.ReadFile("assets/" + n)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// BundledFontDir exposes the materialised font dir for tests that drive the typst
// CLI (which can only take a path).
func BundledFontDir() (string, error) { return bundledFontDir() }
