package pptx

import (
	"math"
	"os"
	"path/filepath"
	"strings"

	"xy/internal/chgk/fontfile"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// Measurement is how the shrink-to-fit pass decides whether text fits, and it is
// the one part of the pptx export that cannot be byte-identical to chgksuite's.
//
// chgksuite measures with Pillow, which — when the machine has libraqm — shapes
// through HarfBuzz and applies GPOS kerning. Advances match this code exactly
// (unhinted, scaled to the pixel size, quantised to 1/64), and so does everything
// with no kerning pair in it; a string that kerns comes out within a fraction of
// a percent. That only changes the outcome when the text lands within that
// fraction of a line boundary, where it costs one point of font size.
//
// A machine without libraqm makes Pillow fall back to integer per-glyph advances,
// which differ from both. chgksuite's own layout is therefore not reproducible
// across machines either; this is as close as a Go port sensibly gets.

// fontFaces are the four styles a measurement needs.
type fontFaces struct {
	regular, bold, italic, boldItalic *measuredFace
}

type measuredFace struct {
	font *sfnt.Font
	path string
	// faces are cached per pixel size, which is what Pillow's own lru_cache does.
	faces map[int]font.Face
	// hheaHeight and unitsPerEm are what a line's height is scaled from.
	hheaHeight, unitsPerEm int
}

func newMeasuredFace(path string) (*measuredFace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := sfnt.Parse(data)
	if err != nil {
		return nil, err
	}
	face := &measuredFace{font: f, path: path, faces: map[int]font.Face{}}
	face.unitsPerEm, face.hheaHeight = verticalMetrics(data)
	return face, nil
}

// verticalMetrics reads head.unitsPerEm and hhea's ascender, descender and line
// gap straight out of the file: the height a line is given is their sum, and
// nothing else in the stack reports it unrounded.
func verticalMetrics(data []byte) (unitsPerEm, height int) {
	tables := fontfile.Tables(data)
	head, hhea := tables["head"], tables["hhea"]
	if len(head) < 20 || len(hhea) < 12 {
		return 0, 0
	}
	unitsPerEm = int(fontfile.Be16(head[18:]))
	ascender := int(int16(fontfile.Be16(hhea[4:])))
	descender := int(int16(fontfile.Be16(hhea[6:])))
	lineGap := int(int16(fontfile.Be16(hhea[8:])))
	return unitsPerEm, ascender - descender + lineGap
}

func (m *measuredFace) face(pixels int) (font.Face, error) {
	if f, ok := m.faces[pixels]; ok {
		return f, nil
	}
	// Size in points at 72 dpi is size in pixels, and unhinted is what matches
	// Pillow: hinting rounds every advance to a whole pixel.
	f, err := opentype.NewFace(m.font, &opentype.FaceOptions{
		Size: float64(pixels), DPI: 72, Hinting: font.HintingNone,
	})
	if err != nil {
		return nil, err
	}
	m.faces[pixels] = f
	return f, nil
}

// fontPixelSize is _font_pixel_size: points at 96 dpi, rounded.
func fontPixelSize(size float64) int {
	return max(1, int(math.Round(size*pxPerInch/ptPerInch)))
}

// width is _measure_text_width_px.
func (m *measuredFace) width(text string, size float64) float64 {
	if text == "" {
		return 0
	}
	f, err := m.face(fontPixelSize(size))
	if err != nil {
		return 0
	}
	return float64(font.MeasureString(f, text)) / 64
}

// lineHeight is _measure_line_height_px, which reads Pillow's font.height —
// FreeType's scaled face height. FreeType scales the horizontal header's own
// ascender − descender + lineGap in one go and rounds the result to the nearest
// pixel; x/image rounds each of the three separately and can land a pixel out,
// which is a line more or fewer on a full slide. So the header is read directly.
func (m *measuredFace) lineHeight(size float64) float64 {
	pixels := float64(fontPixelSize(size))
	if m.unitsPerEm == 0 {
		return pixels
	}
	return math.Round(float64(m.hheaHeight) * pixels / float64(m.unitsPerEm))
}

// pick is _get_measurement_font_path_for_style: the nearest face there is.
func (f *fontFaces) pick(bold, italic bool) *measuredFace {
	var face *measuredFace
	switch {
	case bold && italic:
		face = first(f.boldItalic, f.bold, f.italic)
	case bold:
		face = f.bold
	case italic:
		face = f.italic
	default:
		face = f.regular
	}
	return first(face, f.regular, f.bold, f.italic, f.boldItalic)
}

func (f *fontFaces) empty() bool { return f.pick(false, false) == nil }

func first(faces ...*measuredFace) *measuredFace {
	for _, f := range faces {
		if f != nil {
			return f
		}
	}
	return nil
}

// FindFontFaces looks a font family up by name in the usual directories, the way
// chgksuite reads the system font tables. dirs may name extra places to look.
func FindFontFaces(name string, dirs []string) *fontFaces {
	if name == "" {
		return &fontFaces{}
	}
	// _find_font_faces: a spec naming a file is that file, whatever it is called.
	if info, err := os.Stat(name); err == nil && !info.IsDir() {
		if face, err := newMeasuredFace(name); err == nil {
			return &fontFaces{regular: face}
		}
	}
	search := append([]string{}, dirs...)
	search = append(search,
		"/usr/share/fonts", "/usr/local/share/fonts",
		filepath.Join(os.Getenv("HOME"), ".local/share/fonts"),
		filepath.Join(os.Getenv("HOME"), ".fonts"),
		"/Library/Fonts", "/System/Library/Fonts",
		`C:\Windows\Fonts`,
	)
	want := normalizeFontName(name)
	found := map[string]string{}
	for _, dir := range search {
		if dir == "" {
			continue
		}
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // an unreadable directory is simply not searched
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".ttf" && ext != ".otf" {
				return nil
			}
			base := normalizeFontName(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
			style, ok := strings.CutPrefix(base, want)
			if !ok {
				return nil
			}
			if _, seen := found[style]; !seen {
				found[style] = path
			}
			return nil
		})
	}
	faces := &fontFaces{}
	for style, path := range found {
		face, err := newMeasuredFace(path)
		if err != nil {
			continue
		}
		switch style {
		case "", "regular", "mt":
			faces.regular = face
		case "bold", "bd", "boldmt":
			faces.bold = face
		case "italic", "i", "italicmt":
			faces.italic = face
		case "bolditalic", "bi", "boldItalicmt", "bolditalicmt":
			faces.boldItalic = face
		}
	}
	return faces
}

func normalizeFontName(s string) string {
	return strings.ToLower(strings.NewReplacer(" ", "", "-", "", "_", "").Replace(s))
}

// fontName is _docx_font_name, which the pptx export shares: a --font naming a
// file is that file's family, and anything else is taken as the family itself.
func fontName(spec string) (string, error) {
	if spec == "" {
		return "", nil
	}
	path := spec
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	if abs, err := filepath.Abs(path); err == nil {
		if info, err := os.Stat(abs); err == nil && !info.IsDir() {
			return fontfile.Family(abs)
		}
	}
	return spec, nil
}
