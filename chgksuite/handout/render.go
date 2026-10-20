package handout

import (
	"bytes"
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

// fontFileMode is for the bundled fonts, which are not secret.
const (
	fontFileMode = 0o644
	fontDirMode  = 0o755
)

// bundledFontDir materialises the embedded Noto Sans fonts once per process and
// returns their directory (passed to typst's --font-path). It is the same
// directory in the user's cache every time, so runs reuse it instead of each
// leaving a copy behind.
var bundledFontDir = sync.OnceValues(func() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "chgksuite", "fonts")
	if err := os.MkdirAll(dir, fontDirMode); err != nil {
		return "", err
	}
	for _, n := range fontNames {
		b, err := fontFS.ReadFile("assets/" + n)
		if err != nil {
			return "", err
		}
		if err := writeFontFile(filepath.Join(dir, n), b); err != nil {
			return "", err
		}
	}
	return dir, nil
})

// writeFontFile leaves a file that already holds b alone, and otherwise
// replaces it by a rename, so that another process reading the font never sees
// it half-written.
func writeFontFile(path string, b []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, b) {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".font-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), fontFileMode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Render typesets the .hndt source to a PDF. images are the pictures it
// references, by the name used in the source.
//
// ts decides where typst runs: the server passes the in-memory wasm typesetter, so
// the decrypted questions never reach a filesystem.
func Render(ctx context.Context, hndt string, images map[string][]byte, a Args, ts Typesetter) ([]byte, error) {
	typ, err := prepare(ctx, hndt, images, a, ts)
	if err != nil {
		return nil, err
	}
	pdf, _, err := ts.Compile(ctx, typ, true)
	if err != nil {
		return nil, fmt.Errorf("typst compile failed: %w", err)
	}
	return pdf, nil
}

// SVGTypesetter is a Typesetter that can also typeset to one SVG per page.
type SVGTypesetter interface {
	Typesetter
	SVG(ctx context.Context, typ string) ([][]byte, error)
}

// RenderSVG is Render with the pages as SVGs, for a browser that cannot show
// the PDF inline.
func RenderSVG(ctx context.Context, hndt string, images map[string][]byte, a Args, ts SVGTypesetter) ([][]byte, error) {
	typ, err := prepare(ctx, hndt, images, a, ts)
	if err != nil {
		return nil, err
	}
	pages, err := ts.SVG(ctx, typ)
	if err != nil {
		return nil, fmt.Errorf("typst compile failed: %w", err)
	}
	return pages, nil
}

func prepare(ctx context.Context, hndt string, images map[string][]byte, a Args, ts Typesetter) (string, error) {
	hndt, err := ApplyPreamble(hndt, a)
	if err != nil {
		return "", err
	}
	hndt, images, err = ApplyRotation(hndt, images)
	if err != nil {
		return "", err
	}
	hndt, images = flattenImagePaths(hndt, images)
	if err := ts.SetImages(ctx, images); err != nil {
		return "", err
	}
	return GenerateTyp(hndt, a), nil
}

// BundledFonts returns the embedded Noto Sans faces as bytes. The wasm typst
// (typstwasm) serves fonts from memory, so it needs the bytes rather
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

// RegularFont is the bundled Noto Sans Regular, for an exporter that has to
// measure text the way it will be set.
func RegularFont() []byte {
	b, err := fontFS.ReadFile("assets/NotoSans-Regular.ttf")
	if err != nil {
		panic(err) // embedded at build time
	}
	return b
}
