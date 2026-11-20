package xyimage

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	chgkstrings "code.pecheny.me/pecheny/dopesuite/chgksuite/i18nstrings"
	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
)

// FontsDir is where a Font Family's zip lives beside the pictures that use
// it: in a .4s export's archive and in the images an exporter is handed, as
// FontsDir + "<sha256>.zip" (ADR-0025). A Bundle's fonts/ section is the same.
const FontsDir = "fonts/"

// FontKeyPrefix marks, among the files handed to a typesetter, a font rather
// than an image: "font/<family>" is a Face to set text in under that family
// name, for the one generation. A plain file name never holds a slash.
const FontKeyPrefix = "font/"

// maxFaceBytes is the most a single Face may unpack to (ADR-0025 caps an
// upload at about 20 MB a Face); a zip claiming more is refused rather than
// inflated.
const maxFaceBytes = 24 << 20

var zipHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

// FontZipKey is where the Family zip with this hash is found.
func FontZipKey(hash string) string { return FontsDir + hash + ".zip" }

// IsFontZipKey reports whether a file name is a Family zip's, and so not a
// picture.
func IsFontZipKey(name string) bool {
	h, ok := strings.CutPrefix(name, FontsDir)
	h, zipped := strings.CutSuffix(h, ".zip")
	return ok && zipped && zipHash.MatchString(h)
}

// MissingFontError is an export that needs a Face it was not given. A vector
// rendition without it would set the text in another font and break the
// lines the editor froze, so the export fails instead (ADR-0025).
type MissingFontError struct {
	Font string // the Family as the user named it, or the file inside it
}

func (e *MissingFontError) Error() string {
	return fmt.Sprintf("xyimage: font %q is missing", e.Font)
}

// Faces is the uploaded Faces the vector rendition sets its text in, as the
// files a typesetter is handed: FontKeyPrefix + family → the font file, read
// out of the Family zips in files (keyed by FontZipKey).
func (f *File) Faces(files map[string][]byte) (map[string][]byte, error) {
	if len(f.Bake.Fonts) == 0 {
		return nil, nil
	}
	out := make(map[string][]byte, len(f.Bake.Fonts))
	readers := map[string]*zip.Reader{}
	for _, face := range f.Bake.Fonts {
		zr, err := familyZip(readers, files, face.Zip)
		if err != nil {
			return nil, missing(face)
		}
		data, err := readFace(zr, face.Path)
		if err != nil {
			return nil, missing(face)
		}
		out[FontKeyPrefix+face.Family] = data
	}
	return out, nil
}

func missing(face Face) error {
	name := face.Name
	if name == "" {
		name = face.Path
	}
	return &MissingFontError{Font: name}
}

func familyZip(cache map[string]*zip.Reader, files map[string][]byte, hash string) (*zip.Reader, error) {
	if zr, ok := cache[hash]; ok {
		return zr, nil
	}
	data, ok := files[FontZipKey(hash)]
	if !ok {
		return nil, errors.New("no zip")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	cache[hash] = zr
	return zr, nil
}

func readFace(zr *zip.Reader, path string) ([]byte, error) {
	for _, zf := range zr.File {
		if zf.Name != path {
			continue
		}
		if zf.UncompressedSize64 > maxFaceBytes {
			return nil, errors.New("face too large")
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, maxFaceBytes))
	}
	return nil, errors.New("no face")
}

// Vector is an Edited Image's vector rendition and the Faces it
// needs, keyed for a typesetter (FontKeyPrefix). Anything that is not
// a readable Edited Image gives no SVG and no error: it goes in as a picture.
// The only error is a Face missing from zips.
func Vector(data []byte, zips map[string][]byte) ([]byte, map[string][]byte, error) {
	if !Is(data) {
		return nil, nil, nil
	}
	f, err := Parse(data)
	if err != nil {
		return nil, nil, nil
	}
	svg, err := f.VectorSVG()
	if err != nil {
		return nil, nil, nil
	}
	faces, err := f.Faces(zips)
	if err != nil {
		return nil, nil, err
	}
	return svg, faces, nil
}

// UserError words a missing Face for the person exporting, naming the
// picture and the font; any other error passes through.
func UserError(name string, err error) error {
	var mf *MissingFontError
	if errors.As(err, &mf) {
		return corei18n.User(chgkstrings.Default.Docs.Image.FontMissing(name, mf.Font))
	}
	return fmt.Errorf("%s: %w", name, err)
}
