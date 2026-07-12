// Package chgkimport turns a question package (.docx, .4s or a .zip of both)
// into 4s source plus the images it references — the whole chgksuite `parse`
// pipeline, in-process.
//
// It is the inverse of the export path: xy stores one 4s chunk per card, so the
// caller splits the returned source on blank lines to get its cards, and matches
// the returned images to the `(img …)` directives inside them.
package chgkimport

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"xy/internal/chgk/docxread"
	"xy/internal/chgk/fsource"
	"xy/internal/chgk/textparse"
)

// Image is one image referenced by the package, named as the 4s source's
// `(img …)` directives refer to it.
type Image struct {
	Name string
	MIME string
	Data []byte
}

// Result is a parsed package.
type Result struct {
	// Name is the package name derived from the filename, for the new list.
	Name string
	// Source is the 4s document.
	Source string
	// Images are the images the source references, if any.
	Images []Image
}

// ErrUnsupported is returned for a file extension we can't import.
var ErrUnsupported = errors.New("unsupported file type")

var imageExts = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png",
	".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp",
	".tif": "image/tiff", ".tiff": "image/tiff", ".svg": "image/svg+xml",
}

// Parse imports one uploaded file. filename decides the format and gives the
// package (and image-prefix) its name.
func Parse(filename string, data []byte) (*Result, error) {
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".4s":
		res := &Result{Name: base, Source: normalizeNewlines(string(data))}
		return res, validate(res)
	case ".docx":
		return parseDocx(base, data)
	case ".zip":
		res, err := parseZip(base, data)
		if err != nil {
			return nil, err
		}
		return res, validate(res)
	}
	return nil, fmt.Errorf("%w: %s", ErrUnsupported, filepath.Ext(filename))
}

// validate runs a .4s through the 4s parser so a file that isn't really 4s
// (or that holds nothing importable) fails here rather than becoming an empty
// list. A .docx skips this: it came out of Compose and is 4s by construction.
func validate(r *Result) error {
	if len(fsource.Parse(r.Source, "chgk")) == 0 {
		return errors.New("в файле не найдено вопросов")
	}
	return nil
}

// parseDocx runs the full chgksuite pipeline: docx → plain text → structure → 4s.
func parseDocx(base string, data []byte) (*Result, error) {
	// chgksuite prefixes extracted image names with the source's basename, so the
	// (img …) directives and the image names below agree.
	prefix := strings.ReplaceAll(base, " ", "_") + "_"
	text, imgs, err := docxread.ToText(data, docxread.Options{ImagePrefix: prefix})
	if err != nil {
		return nil, fmt.Errorf("read docx: %w", err)
	}
	source := fsource.Compose(textparse.Parse(text, textparse.Options{}), fsource.NumbersDefault)
	res := &Result{Name: base, Source: source}
	for _, img := range imgs {
		res.Images = append(res.Images, Image{
			Name: img.Name,
			MIME: mimeOf(img.Name),
			Data: img.Data,
		})
	}
	return res, nil
}

// parseZip imports a package shipped as one .4s plus its images, so a user can
// upload questions and handouts in a single file.
func parseZip(base string, data []byte) (*Result, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("read zip: %w", err)
	}
	res := &Result{Name: base}
	var sources []string
	// Images are addressed by the name the (img …) directives use, so remember the
	// archive path each one came from — a package may reference "images/q3.png".
	paths := map[string]string{}
	budget := int64(maxInflated)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := path.Base(f.Name)
		// Skip the junk archivers add (__MACOSX/, .DS_Store, dotfiles).
		if strings.HasPrefix(name, ".") || strings.HasPrefix(f.Name, "__MACOSX/") {
			continue
		}
		ext := strings.ToLower(path.Ext(name))
		mimeType, isImage := imageExts[ext]
		switch {
		case ext == ".4s":
			body, err := readZipEntry(f, &budget)
			if err != nil {
				return nil, err
			}
			sources = append(sources, normalizeNewlines(string(body)))
			// A single .4s names the package; several keep the archive's name.
			if len(sources) == 1 {
				res.Name = strings.TrimSuffix(name, ext)
			} else {
				res.Name = base
			}
		case isImage:
			// Two images with the same base name are indistinguishable to an
			// (img …) directive, so the first one wins rather than silently
			// repointing the directive at the wrong picture.
			if _, dup := paths[name]; dup {
				continue
			}
			body, err := readZipEntry(f, &budget)
			if err != nil {
				return nil, err
			}
			paths[name] = f.Name
			res.Images = append(res.Images, Image{Name: name, MIME: mimeType, Data: body})
		}
	}
	if len(sources) == 0 {
		return nil, errors.New("в архиве нет файла .4s")
	}
	res.Source = strings.Join(sources, "\n\n")
	// A directive may point at the archive path ("(img images/q3.png)") while the
	// image is stored under its base name; rewrite those so they resolve.
	for name, p := range paths {
		if p != name {
			res.Source = replaceImageRef(res.Source, p, name)
		}
	}
	return res, nil
}

// reImgDirective matches an (img …) directive. Its argument is the chgksuite
// "parseimg" form: optional w=/h=/big/inline options, then the filename last.
var reImgDirective = regexp.MustCompile(`\(img\b[^)]*\)`)

// replaceImageRef swaps one image name for another inside (img …) directives
// only, so a name that also occurs in the question text (or is a substring of a
// longer name) can't be corrupted.
func replaceImageRef(source, from, to string) string {
	// The bare "(img NAME)" form first, by exact text: it is what the docx reader
	// emits, and it is the one case the regex below can't see, since a name with a
	// ')' in it is precisely what we are here to fix.
	source = strings.ReplaceAll(source, "(img "+from+")", "(img "+to+")")
	// Then the form with options, e.g. "(img w=8 q3.png)".
	return reImgDirective.ReplaceAllStringFunc(source, func(d string) string {
		inner := strings.TrimSuffix(strings.TrimPrefix(d, "(img"), ")")
		fields := strings.Fields(inner)
		if len(fields) == 0 || fields[len(fields)-1] != from {
			return d
		}
		fields[len(fields)-1] = to
		return "(img " + strings.Join(fields, " ") + ")"
	})
}

// SafeImageNames renames images so xy can actually resolve them, rewriting the
// `(img …)` directives to match.
//
// chgksuite derives an extracted image's name from the source filename, so a
// package called "Кубок (48в).docx" yields `(img Кубок_(48в)_001.png)`. But an
// (img …) directive ends at the first ')' and splits its options on whitespace,
// so such a name is truncated by every reader of the format — chgksuite's own
// included. Import is the one moment we can fix it, since the name is ours to
// choose and nothing references it yet.
func (r *Result) SafeImageNames() {
	taken := make(map[string]bool, len(r.Images))
	for _, img := range r.Images {
		taken[img.Name] = true
	}
	for i := range r.Images {
		old := r.Images[i].Name
		safe := safeImageName(old)
		if safe == old {
			continue
		}
		for taken[safe] {
			safe = "_" + safe
		}
		taken[safe] = true
		r.Images[i].Name = safe
		// Rewrite the directives only — a blind ReplaceAll over the whole source
		// would also hit the name where it appears in ordinary text, or inside a
		// longer name it happens to be a substring of.
		r.Source = replaceImageRef(r.Source, old, safe)
	}
}

// safeImageName strips the characters that would truncate an (img …) directive.
func safeImageName(name string) string {
	return strings.Map(func(rn rune) rune {
		switch rn {
		case '(', ')', '/', '\\':
			return '_'
		}
		if rn == ' ' || rn == '\t' || rn == '\n' || rn == '\r' || rn == 0x00a0 {
			return '_'
		}
		return rn
	}, name)
}

// maxInflated bounds the TOTAL uncompressed size of an archive. Capping each
// member individually is not enough: a few hundred members that each squeak under
// the limit still add up to a zip bomb, since they all land in memory at once.
const maxInflated = 256 << 20

// readZipEntry inflates one member, drawing from the archive's shared budget.
func readZipEntry(f *zip.File, budget *int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", f.Name, err)
	}
	defer rc.Close()
	body, err := io.ReadAll(io.LimitReader(rc, *budget+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", f.Name, err)
	}
	if int64(len(body)) > *budget {
		return nil, errors.New("архив слишком велик в распакованном виде")
	}
	*budget -= int64(len(body))
	return body, nil
}

func mimeOf(name string) string {
	if m, ok := imageExts[strings.ToLower(path.Ext(name))]; ok {
		return m
	}
	if m := mime.TypeByExtension(path.Ext(name)); m != "" {
		return m
	}
	return "application/octet-stream"
}

// normalizeNewlines matches the export path: browsers and Windows editors send
// CRLF, and the 4s parser splits on "\n\n".
func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}
