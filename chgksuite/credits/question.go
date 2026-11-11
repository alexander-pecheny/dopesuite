package credits

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/chgkimport"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/fsource"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/listexport"
)

// ErrNoQuestion is a packet with no question under the number asked for, or
// with more than one (tours numbered from 1 each): nothing is guessed.
var ErrNoQuestion = errors.New("no such question")

// QuestionText is one question of a packet: its number as the packet writes it
// and the question in 4s (every field, the number left out), for a renderer of
// 4s to draw. File is the document it was read from, Language that document's
// language as chgksuite's --language code, "ru" for Russian. Images are the
// pictures its (img …) directives name that the file or its archive holds:
// a .docx's, a .doc's (docread), an .rtf's (rtfread), a PDF's (placed by
// pdftohtml, pdfimages.go), those beside a .4s in an archive; raster ones only,
// within the caps below. A picture left out is drawn as missing.
type QuestionText struct {
	Number   string          `json:"number"`
	Source   string          `json:"source"`
	Language string          `json:"language"`
	File     string          `json:"file"`
	Images   []QuestionImage `json:"images"`
}

// QuestionImage is one picture, named as the (img …) directive names it. Data
// goes into JSON as base64.
type QuestionImage struct {
	Name string `json:"name"`
	MIME string `json:"mime"`
	Data []byte `json:"data"`
}

// The caps on a question's pictures: a page shows them, a site stores them.
const (
	maxQuestionImages     = 20
	maxQuestionImageBytes = 5 << 20
	maxQuestionImagesSize = 15 << 20
)

// pictureMIME is the type of a raster picture a browser draws by its name, ""
// for anything else: an SVG may carry script, a TIFF draws nowhere.
func pictureMIME(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	}
	return ""
}

// questionImages are the pictures source names, out of the file's.
func questionImages(source string, f file) []QuestionImage {
	byName := map[string]chgkimport.Image{}
	for _, img := range f.images {
		if _, ok := byName[img.Name]; !ok {
			byName[img.Name] = img
		}
	}
	out := []QuestionImage{}
	total := 0
	for _, name := range listexport.ImageRefs(source) {
		img, ok := byName[name]
		mime := pictureMIME(name)
		if !ok || mime == "" || len(img.Data) == 0 || len(img.Data) > maxQuestionImageBytes ||
			total+len(img.Data) > maxQuestionImagesSize || len(out) == maxQuestionImages {
			continue
		}
		total += len(img.Data)
		out = append(out, QuestionImage{Name: name, MIME: mime, Data: img.Data})
	}
	return out
}

// Question reads the question numbered number out of an uploaded file, read as
// Read reads it: the same formats, and in an archive the same files picked as
// the packet. The number must equal the packet's own, as parsed; questions are
// never counted.
func Question(name string, data []byte, number string, opt Options) (*QuestionText, error) {
	opt.images = true
	files, err := packetFiles(name, data, opt)
	if err != nil {
		return nil, err
	}
	number = strings.TrimSpace(number)
	var found []QuestionText
	for _, f := range files {
		for _, p := range f.doc {
			q, ok := p.Content.(*fsource.Question)
			if p.Type != "Question" || !ok || !q.Has("number") || strings.TrimSpace(fmt.Sprint(q.Get("number"))) != number {
				continue
			}
			language := f.language
			if language == "" {
				language = "ru"
			}
			source := strings.TrimSpace(fsource.Compose(fsource.Doc{p}, fsource.NumbersNone))
			found = append(found, QuestionText{
				Number:   number,
				Source:   source,
				Language: language,
				File:     f.name,
				Images:   questionImages(source, f),
			})
		}
	}
	if len(found) != 1 {
		return nil, fmt.Errorf("%w: %d questions numbered %q", ErrNoQuestion, len(found), number)
	}
	return &found[0], nil
}

// packetFiles are the files Read takes the credits from.
func packetFiles(name string, data []byte, opt Options) ([]file, error) {
	ext := strings.ToLower(path.Ext(name))
	switch {
	case slices.Contains(archives, ext):
		return archiveFiles(ext, data, opt)
	case slices.Contains(documents, ext):
		f, err := readOne(name, data, opt)
		if err != nil {
			return nil, err
		}
		return []file{f}, nil
	}
	return nil, fmt.Errorf("%w: %s", chgkimport.ErrUnsupported, path.Ext(name))
}
