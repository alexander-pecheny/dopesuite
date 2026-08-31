package docx

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"xy/internal/chgk/fontfile"
)

// --font and --docx_template. The template the export builds on is normally the
// embedded one; --docx_template names another, and --font rewrites whichever it
// is so Word asks for a different family.

var (
	reEmbedFont     = regexp.MustCompile(`<w:embed(?:Regular|Bold|Italic|BoldItalic)\b[^>]*/>`)
	reEmbedTrueType = regexp.MustCompile(`<w:embedTrueTypeFonts\s*/>`)
	reODTTFDefault  = regexp.MustCompile(`<Default Extension="odttf"[^>]*/>`)
)

// fontName is _docx_font_name: a --font naming a file is that file's family,
// and anything else is taken as the family itself.
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

// replaceFont is replace_font_in_docx: every XML part of the template gets the
// new family in place of the three the template names, and the embedded faces
// go with it — leave them and Word would draw the new family's name with Noto
// Sans's glyphs.
func replaceFont(template []byte, font string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(template), int64(len(template)))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "word/fonts/") || f.Name == "word/_rels/fontTable.xml.rels" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		if strings.HasSuffix(f.Name, ".xml") {
			s := string(data)
			s = reEmbedFont.ReplaceAllString(s, "")
			s = reEmbedTrueType.ReplaceAllString(s, "")
			s = reODTTFDefault.ReplaceAllString(s, "")
			s = strings.NewReplacer("Arial Unicode MS", font, "Arial", font, "Noto Sans", font).Replace(s)
			if f.Name == "word/fontTable.xml" {
				s = dedupeFonts(s, font)
			}
			data = []byte(s)
		}
		w, err := zw.Create(f.Name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// template is what the export builds on: the embedded one unless --docx_template
// names another, with --font applied unless it names the family already there.
func (o Options) template() ([]byte, error) {
	data := templateDocx
	if o.Template != nil {
		data = o.Template
	}
	name, err := fontName(o.Font)
	if err != nil {
		return nil, err
	}
	if name == "" || normalizeFontName(name) == "notosans" {
		return data, nil
	}
	return replaceFont(data, name)
}

var reFontEntry = regexp.MustCompile(`(?s)<w:font w:name="([^"]*)"(?:[^>]*/>|[^>]*>.*?</w:font>)`)

// dedupeFonts drops the second hint the rename creates: the template names
// Arial and Noto Sans separately, so renaming both leaves the table asking for
// the same family twice. Only that name is deduplicated — the template's own
// repeats (it lists Times New Roman twice) are its business, and python-docx
// keeps them too.
func dedupeFonts(s, font string) string {
	seen := false
	return reFontEntry.ReplaceAllStringFunc(s, func(entry string) string {
		if reFontEntry.FindStringSubmatch(entry)[1] != font {
			return entry
		}
		if seen {
			return ""
		}
		seen = true
		return entry
	})
}

func normalizeFontName(s string) string {
	return strings.ToLower(strings.NewReplacer(" ", "", "-", "", "_", "").Replace(s))
}
